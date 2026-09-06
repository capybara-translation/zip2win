package zipwin

import (
	"archive/zip"
	"bytes"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// mustWrite は path にファイルを作る。親ディレクトリがなければ作る。
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readZip は ZIP を開き、エントリ名 -> *zip.File のマップを返す。
func readZip(t *testing.T, path string) map[string]*zip.File {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip %s: %v", path, err)
	}
	t.Cleanup(func() { r.Close() })
	m := make(map[string]*zip.File, len(r.File))
	for _, f := range r.File {
		m[f.Name] = f
	}
	return m
}

// readEntry はエントリの内容を文字列で返す。
func readEntry(t *testing.T, f *zip.File) string {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func entryNames(m map[string]*zip.File) []string {
	return slices.Sorted(maps.Keys(m))
}

func TestCreate_DirectoryIncludesRootFolderName(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, "readme.txt"), "hello")
	mustWrite(t, filepath.Join(src, "sub", "日本語資料.txt"), "こんにちは")
	mustWrite(t, filepath.Join(src, "製品 写真.jpg"), "jpg")
	if err := os.MkdirAll(filepath.Join(src, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "out.zip")

	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries := readZip(t, dst)
	want := []string{
		"docs/",
		"docs/empty/",
		"docs/readme.txt",
		"docs/sub/",
		"docs/sub/日本語資料.txt",
		"docs/製品 写真.jpg",
	}
	if got := entryNames(entries); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	for name, f := range entries {
		if f.Flags&utf8Flag == 0 {
			t.Errorf("%q: EFS flag not set (flags=%#04x)", name, f.Flags)
		}
		if f.NonUTF8 {
			t.Errorf("%q: NonUTF8 is true", name)
		}
		if !strings.HasSuffix(name, "/") && f.Method != zip.Deflate {
			t.Errorf("%q: method = %d, want Deflate", name, f.Method)
		}
	}
	if got := readEntry(t, entries["docs/sub/日本語資料.txt"]); got != "こんにちは" {
		t.Errorf("content = %q, want こんにちは", got)
	}
}

func TestCreate_NormalizesNFDToNFC(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	// "か" + 結合濁点 (NFD)。NFC では "が" (U+304C) 1 文字になる。
	mustWrite(t, filepath.Join(src, norm.NFD.String("が.txt")), "x") // か + 結合濁点 (U+304B U+3099)
	dst := filepath.Join(tmp, "out.zip")

	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries := readZip(t, dst)
	if _, ok := entries["docs/が.txt"]; !ok {
		t.Errorf("NFC name not found; entries = %v", entryNames(entries))
	}
}

func TestCreate_NFCCollisionIsError(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, "が.txt"), "nfc")                  // NFC (U+304C)
	mustWrite(t, filepath.Join(src, norm.NFD.String("が.txt")), "nfd") // NFD (U+304B U+3099)
	names, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	// APFS/HFS+ は正規化差を同名として扱うので 2 つ目の書き込みが 1 つ目を上書きし、衝突が起きない。
	if len(names) < 2 {
		t.Skip("filesystem merges NFC/NFD names; collision cannot occur here")
	}

	err = Create(CreateOptions{Source: src, Dest: filepath.Join(tmp, "out.zip")})
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Errorf("err = %v, want collision error", err)
	}
}

func TestCreate_SingleFile(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "メモ.txt")
	mustWrite(t, src, "memo")
	dst := filepath.Join(tmp, "out.zip")

	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries := readZip(t, dst)
	if got := entryNames(entries); !slices.Equal(got, []string{"メモ.txt"}) {
		t.Errorf("entries = %v, want [メモ.txt]", got)
	}
	if got := readEntry(t, entries["メモ.txt"]); got != "memo" {
		t.Errorf("content = %q, want memo", got)
	}
}

func TestCreate_RejectsSymlinkSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	mustWrite(t, filepath.Join(real, "a.txt"), "a")
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	err := Create(CreateOptions{Source: link, Dest: filepath.Join(tmp, "out.zip")})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("err = %v, want 'symbolic link' error", err)
	}
}

func TestCreate_MissingSource(t *testing.T) {
	tmp := t.TempDir()
	err := Create(CreateOptions{Source: filepath.Join(tmp, "nope"), Dest: filepath.Join(tmp, "out.zip")})
	if err == nil {
		t.Fatal("expected error for missing source")
	}
}

func TestCreate_ExcludesMacMetadata(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, "keep.txt"), "k")
	mustWrite(t, filepath.Join(src, ".DS_Store"), "junk")
	mustWrite(t, filepath.Join(src, "._keep.txt"), "junk")
	mustWrite(t, filepath.Join(src, "__MACOSX", "._x"), "junk")
	mustWrite(t, filepath.Join(src, "sub", ".DS_Store"), "junk")
	mustWrite(t, filepath.Join(src, "sub", "b.txt"), "b")
	dst := filepath.Join(tmp, "out.zip")

	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := []string{"docs/", "docs/keep.txt", "docs/sub/", "docs/sub/b.txt"}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

func TestCreate_SkipsSymlinkWithNotice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, "real.txt"), "r")
	if err := os.Symlink(filepath.Join(src, "real.txt"), filepath.Join(src, "link.txt")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "out.zip")
	var stderr bytes.Buffer

	if err := Create(CreateOptions{Source: src, Dest: dst, Stderr: &stderr}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := entryNames(readZip(t, dst)); !slices.Equal(got, []string{"docs/", "docs/real.txt"}) {
		t.Errorf("entries = %v, want [docs/ docs/real.txt]", got)
	}
	if !strings.Contains(stderr.String(), "skipping symbolic link") || !strings.Contains(stderr.String(), "link.txt") {
		t.Errorf("stderr = %q, want symlink notice naming link.txt", stderr.String())
	}
}

func TestCreate_DoesNotIncludeItself(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, "a.txt"), "a")
	dst := filepath.Join(src, "out.zip") // 出力先が入力ディレクトリの中

	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := entryNames(readZip(t, dst)); !slices.Equal(got, []string{"docs/", "docs/a.txt"}) {
		t.Errorf("entries = %v, want [docs/ docs/a.txt]", got)
	}
}

func TestCreate_NilStderrIsAllowed(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, "a.txt"), "a")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(src, "a.txt"), filepath.Join(src, "link.txt")); err != nil {
			t.Fatal(err)
		}
	}
	if err := Create(CreateOptions{Source: src, Dest: filepath.Join(tmp, "out.zip")}); err != nil {
		t.Fatalf("Create with nil Stderr: %v", err)
	}
}

func TestCreate_SkipsSymlinkToDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, "real.txt"), "r")
	mustWrite(t, filepath.Join(src, "sub", "inner.txt"), "i")
	if err := os.Symlink(filepath.Join(src, "sub"), filepath.Join(src, "linkdir")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "out.zip")
	var stderr bytes.Buffer

	if err := Create(CreateOptions{Source: src, Dest: dst, Stderr: &stderr}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := []string{"docs/", "docs/real.txt", "docs/sub/", "docs/sub/inner.txt"}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if !strings.Contains(stderr.String(), "skipping symbolic link") || !strings.Contains(stderr.String(), "linkdir") {
		t.Errorf("stderr = %q, want symlink notice naming linkdir", stderr.String())
	}
}

// TestCreate_DoesNotIncludeItselfViaPathAlias は Source と Dest が同じ実体を
// 別名（macOS の /tmp と /private/tmp など）で参照する場合でも、書きかけの ZIP を
// 自分自身に取り込まないことを確認する。alias 自体は symlink だが、
// alias/docs という末尾コンポーネントは symlink ではないため受理される必要がある。
func TestCreate_DoesNotIncludeItselfViaPathAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	mustWrite(t, filepath.Join(real, "docs", "a.txt"), "a")
	alias := filepath.Join(tmp, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(alias, "docs")           // alias 経由（alias 自体が symlink）
	dst := filepath.Join(real, "docs", "out.zip") // 実パス側に出力

	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := []string{"docs/", "docs/a.txt"}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}
