package zipwin

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"io/fs"
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

// TestCreate_NFCCollisionBetweenDirsIsError はディレクトリ同士の衝突も検出することを確認する。
// seen のキーは末尾 "/" を付ける前の名前なので、ディレクトリとファイルの衝突も拾える。
func TestCreate_NFCCollisionBetweenDirsIsError(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	for _, name := range []string{"が", norm.NFD.String("が")} {
		if err := os.MkdirAll(filepath.Join(src, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	names, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	// APFS/HFS+ は正規化差を同名として扱うので 2 つ目の MkdirAll が既存を指し、衝突が起きない。
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

// TestCreate_MacMetadataSourceIsError は除外ルールに引っかかる名前を Source に指定したとき、
// 黙って空（または中身が抜けた）アーカイブを作らずエラーになることを確認する。
func TestCreate_MacMetadataSourceIsError(t *testing.T) {
	for _, name := range []string{"__MACOSX", ".DS_Store", "._x"} {
		t.Run(name, func(t *testing.T) {
			tmp := t.TempDir()
			src := filepath.Join(tmp, name)
			if name == "__MACOSX" {
				mustWrite(t, filepath.Join(src, "a.txt"), "a")
			} else {
				mustWrite(t, src, "a")
			}

			err := Create(CreateOptions{Source: src, Dest: filepath.Join(tmp, "out.zip")})
			if err == nil || !strings.Contains(err.Error(), "excluded macOS metadata name") {
				t.Errorf("err = %v, want excluded macOS metadata name error", err)
			}
		})
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
	// 名前に端末エスケープシーケンスを仕込んだリンク。通知経由で端末を操作されないことを確認する。
	if err := os.Symlink(filepath.Join(src, "real.txt"), filepath.Join(src, "esc\x1b[31m.txt")); err != nil {
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
	// 名前に ESC を含むエントリでも、端末を操作できる生のエスケープシーケンスは出さない。
	if strings.Contains(stderr.String(), "\x1b") {
		t.Errorf("stderr contains a raw ESC: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), `\u001b[31m`) {
		t.Errorf("stderr = %q, want the ESC in the name escaped", stderr.String())
	}
}

// TestCreate_WarnsOnBackslashInName はファイル名のバックスラッシュを警告することを確認する。
// 一部の展開ツールはこれをディレクトリ区切りとして扱う（zip2win inspect も NG と報告する）。
func TestCreate_WarnsOnBackslashInName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip(`\ cannot appear in a Windows file name`)
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, `a\b.txt`), "x")
	dst := filepath.Join(tmp, "out.zip")
	var stderr bytes.Buffer

	if err := Create(CreateOptions{Source: src, Dest: dst, Stderr: &stderr}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := []string{"docs/", `docs/a\b.txt`}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if !strings.Contains(stderr.String(), "name contains backslash") || !strings.Contains(stderr.String(), `a\b.txt`) {
		t.Errorf("stderr = %q, want a backslash warning naming the entry", stderr.String())
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

// TestCreate_ResolvesSourcePathAliases は Source が symlink 経由の別名パスで与えられても、
// 走査を実体のパスで行うことを確認する（Create の EvalSymlinks(srcAbs) を固定するテスト）。
// 解決しないと通知やエラーが実体と対応しない見かけのパスを指し、
// どのファイルの話なのかを追えなくなる。ルート名は指定どおりのままでなければならない。
func TestCreate_ResolvesSourcePathAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "real")
	mustWrite(t, filepath.Join(realDir, "docs", "a.txt"), "a")
	// 通知を出させるための symlink。通知に載るパスで解決の有無を観測する。
	if err := os.Symlink(filepath.Join(realDir, "docs", "a.txt"), filepath.Join(realDir, "docs", "link.txt")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(tmp, "alias")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Fatal(err)
	}
	// tmp 自体が symlink 経由のことがある (macOS の /var -> /private/var) ので期待値も解決しておく。
	resolved, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "out.zip")
	var stderr bytes.Buffer

	if err := Create(CreateOptions{Source: filepath.Join(alias, "docs"), Dest: dst, Stderr: &stderr}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := filepath.Join(resolved, "docs", "link.txt")
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want the notice to name the resolved path %q", stderr.String(), want)
	}
	if strings.Contains(stderr.String(), alias+string(filepath.Separator)) {
		t.Errorf("stderr = %q, want no unresolved alias path", stderr.String())
	}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, []string{"docs/", "docs/a.txt"}) {
		t.Errorf("entries = %v, want [docs/ docs/a.txt]", got)
	}
}

// TestCreate_DoesNotIncludeItselfCaseInsensitive は大文字小文字を区別しないファイルシステム
// (APFS / NTFS の既定) で、Source と Dest の綴りだけが違う場合でも自己取り込みを防げることを確認する。
// パス文字列の比較では防げず、ファイル実体の同一性 (os.SameFile) で判定する必要がある。
func TestCreate_DoesNotIncludeItselfCaseInsensitive(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "SRC")
	mustWrite(t, filepath.Join(src, "a.txt"), "a")
	// 実際に大文字小文字を無視するファイルシステムかを確認してから進む。
	if _, err := os.Stat(filepath.Join(tmp, "src")); err != nil {
		t.Skip("filesystem is case-sensitive; the alias cannot occur here")
	}
	dst := filepath.Join(tmp, "src", "out.zip") // 同じディレクトリを別の綴りで指す

	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := []string{"SRC/", "SRC/a.txt"}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

// TestCreate_SingleFileSourceIsDestIsError は単一ファイル入力で Source と Dest が
// 同じ実体のとき、rename で元ファイルを ZIP に置き換えて壊さないことを確認する。
func TestCreate_SingleFileSourceIsDestIsError(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "メモ.txt")
	mustWrite(t, src, "memo")

	err := Create(CreateOptions{Source: src, Dest: src, Force: true})
	if err == nil || !strings.Contains(err.Error(), "same file") {
		t.Fatalf("err = %v, want 'same file' error", err)
	}
	if b, _ := os.ReadFile(src); string(b) != "memo" {
		t.Errorf("source file was replaced: %q", b)
	}
}

// TestCreate_SourceIsTempFileIsError は Source が出力先の一時ファイル名 (<dest>.tmp) と
// 同じ実体のとき、--force の取り残し掃除が入力ファイルを消してしまわないことを確認する。
func TestCreate_SourceIsTempFileIsError(t *testing.T) {
	tmp := t.TempDir()
	dst := filepath.Join(tmp, "out.zip")
	src := dst + ".tmp"
	mustWrite(t, src, "precious")

	err := Create(CreateOptions{Source: src, Dest: dst, Force: true})
	if err == nil || !strings.Contains(err.Error(), "same file") {
		t.Fatalf("err = %v, want 'same file' error", err)
	}
	if b, readErr := os.ReadFile(src); readErr != nil || string(b) != "precious" {
		t.Errorf("source file was removed or modified: %q (err=%v)", b, readErr)
	}
	if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("destination should not exist (err=%v)", err)
	}
}

func newSourceDir(t *testing.T, tmp string) string {
	t.Helper()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, "a.txt"), "a")
	return src
}

func TestCreate_DestExistsWithoutForceIsError(t *testing.T) {
	tmp := t.TempDir()
	src := newSourceDir(t, tmp)
	dst := filepath.Join(tmp, "out.zip")
	mustWrite(t, dst, "precious")

	err := Create(CreateOptions{Source: src, Dest: dst})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want 'already exists'", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "precious" {
		t.Errorf("existing file was modified: %q", b)
	}
}

func TestCreate_DestExistsWithForceOverwrites(t *testing.T) {
	tmp := t.TempDir()
	src := newSourceDir(t, tmp)
	dst := filepath.Join(tmp, "out.zip")
	mustWrite(t, dst, "old")

	if err := Create(CreateOptions{Source: src, Dest: dst, Force: true}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, []string{"docs/", "docs/a.txt"}) {
		t.Errorf("entries = %v", got)
	}
}

func TestCreate_DestIsDirectoryIsError(t *testing.T) {
	tmp := t.TempDir()
	src := newSourceDir(t, tmp)
	dst := filepath.Join(tmp, "outdir")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	err := Create(CreateOptions{Source: src, Dest: dst, Force: true})
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("err = %v, want 'is a directory'", err)
	}
}

func TestCreate_StaleTempFileIsError(t *testing.T) {
	tmp := t.TempDir()
	src := newSourceDir(t, tmp)
	dst := filepath.Join(tmp, "out.zip")
	mustWrite(t, dst+".tmp", "stale")

	err := Create(CreateOptions{Source: src, Dest: dst})
	if err == nil || !strings.Contains(err.Error(), "temporary file") {
		t.Fatalf("err = %v, want temporary file error", err)
	}
	// 中断された過去の実行が残した一時ファイルで詰まったとき、抜け道を案内する。
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("err = %v, want the message to mention --force", err)
	}
}

// TestCreate_StaleTempFileRemovedWithForce は取り残された <dest>.tmp が出力先を
// 恒久的に塞がないこと（--force で回復できること）を確認する。
func TestCreate_StaleTempFileRemovedWithForce(t *testing.T) {
	tmp := t.TempDir()
	src := newSourceDir(t, tmp)
	dst := filepath.Join(tmp, "out.zip")
	mustWrite(t, dst+".tmp", "stale")
	var stderr bytes.Buffer

	if err := Create(CreateOptions{Source: src, Dest: dst, Force: true, Stderr: &stderr}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, []string{"docs/", "docs/a.txt"}) {
		t.Errorf("entries = %v, want [docs/ docs/a.txt]", got)
	}
	if _, err := os.Lstat(dst + ".tmp"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("temporary file still exists (err=%v)", err)
	}
	// 他人のファイルを消したように見える事故を避けるため、消したことは黙らない。
	if !strings.Contains(stderr.String(), "removing stale temporary file") || !strings.Contains(stderr.String(), "out.zip.tmp") {
		t.Errorf("stderr = %q, want a notice naming the removed temporary file", stderr.String())
	}
}

// TestCreate_DoesNotIncludeExistingDestWithForce は --force で既存の出力先を上書きするとき、
// 走査中に出会う「上書き前の Dest」を取り込まないことを確認する。
func TestCreate_DoesNotIncludeExistingDestWithForce(t *testing.T) {
	tmp := t.TempDir()
	src := newSourceDir(t, tmp)
	dst := filepath.Join(src, "out.zip") // 出力先が入力ディレクトリの中
	mustWrite(t, dst, "old")

	if err := Create(CreateOptions{Source: src, Dest: dst, Force: true}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := []string{"docs/", "docs/a.txt"}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

func TestCreate_FailureLeavesNoFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0 does not block reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read files regardless of mode")
	}
	tmp := t.TempDir()
	src := newSourceDir(t, tmp)
	unreadable := filepath.Join(src, "secret.txt")
	mustWrite(t, unreadable, "s")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(unreadable, 0o644) })
	dst := filepath.Join(tmp, "out.zip")

	if err := Create(CreateOptions{Source: src, Dest: dst}); err == nil {
		t.Fatal("expected error from unreadable file")
	}
	for _, p := range []string{dst, dst + ".tmp"} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s should not exist after failure (err=%v)", p, err)
		}
	}
}
