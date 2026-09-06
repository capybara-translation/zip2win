// Package zipwin は Windows の標準機能で展開しても文字化けしない ZIP を作成・検査する。
//
// 文字化け対策の中核は次の 3 点:
//   - ファイル名を UTF-8 で格納する
//   - General Purpose Bit Flag の bit 11 (EFS) を立てて UTF-8 であることを明示する
//   - macOS 由来の NFD 名を NFC に正規化する
package zipwin

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// utf8Flag は ZIP の General Purpose Bit Flag の bit 11 (EFS / Language Encoding Flag)。
// Go の Writer は非 ASCII の UTF-8 名なら自動で立てるが、ASCII 名でも明示的に立てる運用にする。
const utf8Flag = 1 << 11

// CreateOptions は Create の入力。
type CreateOptions struct {
	// Source は圧縮対象のディレクトリまたは単一ファイル。
	Source string
	// Dest は出力する ZIP のパス。
	Dest string
	// Stderr はシンボリックリンクをスキップしたときの通知先。nil なら通知しない。
	Stderr io.Writer
}

// Create は opts.Source を ZIP にして opts.Dest に書き出す。
// ディレクトリの場合はそのディレクトリ名が ZIP のルートフォルダになる。
func Create(opts CreateOptions) (retErr error) {
	srcAbs, err := filepath.Abs(opts.Source)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}
	dstAbs, err := filepath.Abs(opts.Dest)
	if err != nil {
		return fmt.Errorf("resolve destination path: %w", err)
	}
	// "/" や "." を渡されるとルートフォルダ名が作れない。
	if base := filepath.Base(srcAbs); base == "." || base == string(filepath.Separator) {
		return fmt.Errorf("source %q has no name to use as the archive root", opts.Source)
	}
	// Lstat: Source 自体が symlink なら追跡せずエラーにする（symlink 非追跡の方針を root にも適用）。
	info, err := os.Lstat(srcAbs)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("source %q is a symbolic link", opts.Source)
	}
	// symlink による別名（macOS の /tmp と /private/tmp など）を解決しておかないと、
	// Source と Dest が実体として同じ場所でも文字列比較では一致せず、
	// 自己取り込み防止（下記 excluded）が効かなくなる。
	// 末尾コンポーネントが symlink でないことは直前の Lstat で確認済みなので、
	// ここで解決してもアーカイブのルート名（filepath.Base）は変わらない。
	srcAbs, err = filepath.EvalSymlinks(srcAbs)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}
	// dstAbs はまだ存在しないことがあるため本体は解決できない。親ディレクトリだけ解決して結合する。
	dstDir, err := filepath.EvalSymlinks(filepath.Dir(dstAbs))
	if err != nil {
		return fmt.Errorf("resolve destination directory: %w", err)
	}
	dstAbs = filepath.Join(dstDir, filepath.Base(dstAbs))

	out, err := os.Create(dstAbs)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer func() {
		if err := out.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("close destination: %w", err)
		}
	}()

	zw := zip.NewWriter(out)
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	c := &creator{
		zw:       zw,
		base:     filepath.Dir(srcAbs),
		excluded: []string{dstAbs},
		stderr:   stderr,
	}
	if err := c.walk(srcAbs, info); err != nil {
		_ = zw.Close()
		return err
	}
	// Close で中央ディレクトリが書かれる。ここを検査しないと壊れた ZIP を成功扱いしてしまう。
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalize zip: %w", err)
	}
	return nil
}

// creator は 1 回の ZIP 作成の状態を持つ。
type creator struct {
	zw *zip.Writer
	// base は ZIP 内パスの基準ディレクトリ。Source の親なので Source 名がルートになる。
	base string
	// excluded は走査中に出会っても取り込まない絶対パス（出力 ZIP 自身など）。
	excluded []string
	// stderr はスキップ通知の出力先。
	stderr io.Writer
}

// walk は srcAbs (ディレクトリまたはファイル) 配下を順に add する。
// 除外対象と symlink はここで弾く。
func (c *creator) walk(srcAbs string, info fs.FileInfo) error {
	if !info.IsDir() {
		return c.add(srcAbs, info)
	}
	return filepath.WalkDir(srcAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// 出力 ZIP が入力ディレクトリ内にあると、書きかけの自分自身を読み込んでしまう。
		if slices.Contains(c.excluded, path) {
			return nil
		}
		if isMacMetadata(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// WalkDir は symlink を辿らない。リンク先の意図しないファイル取り込みやループを避けるため
		// エントリとしても格納せず、黙って欠落しないよう通知だけ出す。
		if d.Type()&fs.ModeSymlink != 0 {
			fmt.Fprintf(c.stderr, "zip2win: skipping symbolic link: %s\n", path)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return c.add(path, info)
	})
}

// isMacMetadata は macOS が生成するメタデータファイル・フォルダ名なら true。
//   - .DS_Store: Finder の表示設定
//   - __MACOSX: AppleDouble などを格納するフォルダ
//   - ._*: AppleDouble のサイドカーファイル
func isMacMetadata(name string) bool {
	return name == ".DS_Store" || name == "__MACOSX" || strings.HasPrefix(name, "._")
}

// entryName は path を base からの相対パスにし、ZIP 用に区切りを / に統一して NFC 正規化する。
func (c *creator) entryName(path string) (string, error) {
	rel, err := filepath.Rel(c.base, path)
	if err != nil {
		return "", err
	}
	// Linux ではファイル名が任意のバイト列になりうる。EFS を立てる以上、不正な UTF-8 は拒否する。
	if !utf8.ValidString(rel) {
		return "", fmt.Errorf("file name is not valid UTF-8: %q", rel)
	}
	return norm.NFC.String(filepath.ToSlash(rel)), nil
}

// add は 1 エントリを ZIP に書く。
func (c *creator) add(path string, info fs.FileInfo) error {
	name, err := c.entryName(path)
	if err != nil {
		return err
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return fmt.Errorf("build header for %s: %w", path, err)
	}
	header.Name = name
	header.NonUTF8 = false
	header.Flags |= utf8Flag

	if info.IsDir() {
		// 末尾 / がディレクトリエントリの印。Writer はサイズを 0 にして Store で書く。
		header.Name += "/"
		_, err := c.zw.CreateHeader(header)
		return err
	}

	header.Method = zip.Deflate
	w, err := c.zw.CreateHeader(header)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(w, f)
	return errors.Join(copyErr, f.Close())
}
