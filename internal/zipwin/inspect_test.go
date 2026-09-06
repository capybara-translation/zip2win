package zipwin

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// writeRawZip は検査用に任意のヘッダを持つ ZIP を作る。Writer は名前を検証しないので
// "../" や重複名のような不正な ZIP も作れる。
func writeRawZip(t *testing.T, path string, headers []*zip.FileHeader) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, h := range headers {
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(h.Name, "/") {
			if _, err := w.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func findEntry(t *testing.T, r *Report, name string) Entry {
	t.Helper()
	for _, e := range r.Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("entry %q not found in %+v", name, r.Entries)
	return Entry{}
}

func hasProblem(e Entry, substr string) bool {
	return slices.ContainsFunc(e.Problems, func(p string) bool { return strings.Contains(p, substr) })
}

func TestInspect_ZipFromCreateIsOK(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, "日本語.txt"), "x")
	mustWrite(t, filepath.Join(src, "ascii.txt"), "x")
	dst := filepath.Join(tmp, "out.zip")
	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatal(err)
	}

	r, err := Inspect(dst)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !r.OK() {
		t.Errorf("report not OK: %+v", r.Entries)
	}
	if len(r.Entries) != 3 {
		t.Errorf("got %d entries, want 3", len(r.Entries))
	}
}

func TestInspect_DetectsProblems(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.zip")
	writeRawZip(t, path, []*zip.FileHeader{
		{Name: "日本語.txt", NonUTF8: true}, // EFS なし
		{Name: "../evil.txt"},            // パストラバーサル
		{Name: "/abs.txt"},               // 絶対パス
		{Name: `dir\file.txt`},           // バックスラッシュ
		{Name: ".DS_Store"},              // Mac メタデータ
		{Name: "sub/._x"},                // Mac メタデータ（サブディレクトリ）
		{Name: "dup.txt"},
		{Name: "dup.txt"}, // 重複
		{Name: "fine.txt", Flags: utf8Flag},
	})

	r, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if r.OK() {
		t.Fatal("report should not be OK")
	}
	cases := []struct{ name, problem string }{
		{"日本語.txt", "EFS"},
		{"../evil.txt", "traversal"},
		{"/abs.txt", "absolute"},
		{`dir\file.txt`, "backslash"},
		{".DS_Store", "macOS"},
		{"sub/._x", "macOS"},
	}
	for _, c := range cases {
		if e := findEntry(t, r, c.name); !hasProblem(e, c.problem) {
			t.Errorf("%q: problems = %v, want one containing %q", c.name, e.Problems, c.problem)
		}
	}
	dups := 0
	for _, e := range r.Entries {
		// バイト単位で同名の重複は、正規化後の重複と区別できるメッセージにする。
		if e.Name == "dup.txt" && slices.Contains(e.Problems, "duplicate entry name") {
			dups++
		}
	}
	if dups != 1 {
		t.Errorf("duplicate flagged %d times, want exactly 1 (the second occurrence)", dups)
	}
	if e := findEntry(t, r, "fine.txt"); len(e.Problems) != 0 {
		t.Errorf("fine.txt should have no problems, got %v", e.Problems)
	}
}

// TestInspect_FlagsNonNFCName は NFD のままのエントリ名を報告することを確認する。
// Windows は NFC 前提で表示するため、NFD 名は結合文字が分かれて見えることがある。
func TestInspect_FlagsNonNFCName(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "nfd.zip")
	nfd := norm.NFD.String("が.txt") // か + 結合濁点 (U+304B U+3099)
	writeRawZip(t, path, []*zip.FileHeader{{Name: nfd, Flags: utf8Flag}})

	r, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if e := findEntry(t, r, nfd); !hasProblem(e, "NFC") {
		t.Errorf("problems = %v, want a not-NFC problem", e.Problems)
	}
}

// TestInspect_FlagsDuplicateAfterNFC は NFC 正規化すると同名になる 2 エントリを
// 重複として報告することを確認する。Windows で展開すると同じパスに書かれる。
func TestInspect_FlagsDuplicateAfterNFC(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "dup.zip")
	nfc := "が.txt"
	nfd := norm.NFD.String(nfc)
	writeRawZip(t, path, []*zip.FileHeader{
		{Name: nfc, Flags: utf8Flag},
		{Name: nfd, Flags: utf8Flag},
	})

	r, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if e := findEntry(t, r, nfc); len(e.Problems) != 0 {
		t.Errorf("first (NFC) entry: problems = %v, want none", e.Problems)
	}
	if e := findEntry(t, r, nfd); !hasProblem(e, "duplicate entry name after NFC normalization") {
		t.Errorf("second (NFD) entry: problems = %v, want duplicate-after-NFC", e.Problems)
	}
}

func TestInspect_ASCIIWithoutEFSIsFlagged(t *testing.T) {
	// 運用ルールとして全エントリ EFS 必須なので、ASCII 名でも EFS なしは NG。
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ascii.zip")
	writeRawZip(t, path, []*zip.FileHeader{{Name: "plain.txt"}})

	r, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if e := findEntry(t, r, "plain.txt"); !hasProblem(e, "EFS") {
		t.Errorf("problems = %v, want EFS problem", e.Problems)
	}
}

func TestInspect_NotAZip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "x.zip")
	mustWrite(t, path, "not a zip")
	if _, err := Inspect(path); err == nil {
		t.Fatal("expected error for non-zip input")
	}
}

func TestReport_FormatEscapesControlChars(t *testing.T) {
	r := &Report{Entries: []Entry{{Name: "a\x1b[31mb.txt", Flags: utf8Flag}}}
	var buf bytes.Buffer
	if err := r.Format(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("output contains raw ESC: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "\\u001b[") {
		t.Errorf("output = %q, want escaped \\u001b", buf.String())
	}
}

func TestDisplayName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"日本語.txt", "日本語.txt"},
		{"a\x1bb", "a\\u001bb"},
		{"bad\xffbyte", `bad\xffbyte`},
	}
	for _, c := range cases {
		if got := displayName(c.in); got != c.want {
			t.Errorf("displayName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
