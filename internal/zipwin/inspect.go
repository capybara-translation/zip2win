package zipwin

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Entry は ZIP 内 1 エントリの検査結果。
type Entry struct {
	Name  string
	Flags uint16
	// Problems は検出した問題。空なら問題なし。
	Problems []string
}

// Report は Inspect の結果。
type Report struct {
	Entries []Entry
}

// OK は全エントリに問題がなければ true。
func (r *Report) OK() bool {
	for _, e := range r.Entries {
		if len(e.Problems) > 0 {
			return false
		}
	}
	return true
}

// Inspect は path の ZIP の中央ディレクトリを読んで各エントリを検査する。展開は行わない。
func Inspect(path string) (*Report, error) {
	zr, err := zip.OpenReader(path)
	// GODEBUG=zipinsecurepath=0 のとき、".." や絶対パスを含む ZIP は ErrInsecurePath 付きで返る。
	// reader 自体は使えるので、検査ツールとしてはそのまま読んで問題を報告する。
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	report := &Report{Entries: make([]Entry, 0, len(zr.File))}
	// 重複判定は NFC 正規化後の名前をキーにする。値は最初に現れた正規化前の名前で、
	// バイト単位の重複と「正規化して初めて衝突する重複」を区別するために持つ。
	// Windows は NFC で名前を扱うため、後者も展開時には同じパスへ書かれてしまう。
	seen := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		e := Entry{Name: f.Name, Flags: f.Flags, Problems: checkName(f.Name, f.Flags)}
		key := norm.NFC.String(f.Name)
		if prev, dup := seen[key]; dup {
			if prev == f.Name {
				e.Problems = append(e.Problems, "duplicate entry name")
			} else {
				e.Problems = append(e.Problems, "duplicate entry name after NFC normalization")
			}
		} else {
			seen[key] = f.Name
		}
		report.Entries = append(report.Entries, e)
	}
	return report, nil
}

// checkName は名前とフラグに関する問題を列挙する。
func checkName(name string, flags uint16) []string {
	var problems []string
	if flags&utf8Flag == 0 {
		problems = append(problems, "EFS flag (bit 11) not set")
	}
	if !utf8.ValidString(name) {
		problems = append(problems, "name is not valid UTF-8")
	} else if norm.NFC.String(name) != name {
		// NFD のままの名前（macOS 由来）は Windows で結合文字が分かれて見えることがあり、
		// NFC の同名エントリと衝突もする。不正な UTF-8 では正規化結果が信用できないので見ない。
		problems = append(problems, "name is not NFC-normalized")
	}
	// 以下は解凍側でパストラバーサルの素材になる名前。
	if strings.Contains(name, `\`) {
		problems = append(problems, "backslash in name")
	}
	// ドライブレターは ASCII 英字 1 文字に限られる。':' だけを見ると "1:2.txt" のような
	// 単なるコロン入りの名前まで絶対パス扱いしてしまう。
	if strings.HasPrefix(name, "/") || (len(name) >= 2 && name[1] == ':' && isASCIILetter(name[0])) {
		problems = append(problems, "absolute path")
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			problems = append(problems, "path traversal (..)")
			break
		}
	}
	for _, seg := range strings.Split(name, "/") {
		if isMacMetadata(seg) {
			problems = append(problems, "macOS metadata file")
			break
		}
	}
	return problems
}

// isASCIILetter は Windows のドライブレターに使える文字なら true。
func isASCIILetter(b byte) bool {
	return ('A' <= b && b <= 'Z') || ('a' <= b && b <= 'z')
}

// Format は人が読める形で結果を w に書き出す。
func (r *Report) Format(w io.Writer) error {
	for _, e := range r.Entries {
		status := "ok"
		if len(e.Problems) > 0 {
			status = "NG: " + strings.Join(e.Problems, "; ")
		}
		efs := e.Flags&utf8Flag != 0
		if _, err := fmt.Fprintf(w, "%-40s EFS=%-5v Flags=%#04x %s\n", displayName(e.Name), efs, e.Flags, status); err != nil {
			return err
		}
	}
	return nil
}

// displayName は端末へ安全に表示できるよう制御文字と不正バイトをエスケープする。
// 悪意ある ZIP のエントリ名にエスケープシーケンスが含まれていても端末を操作されないようにする。
func displayName(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, name[i])
		case unicode.IsControl(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}
