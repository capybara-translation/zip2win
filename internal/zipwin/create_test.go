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

// mustWrite creates a file at path, creating any missing parent directories.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readZip opens the ZIP and returns a map of entry name -> *zip.File.
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

// readEntry returns the entry's content as a string.
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
	// U+304B ("ka") + combining dakuten (NFD). In NFC that becomes the single character U+304C ("ga").
	mustWrite(t, filepath.Join(src, norm.NFD.String("が.txt")), "x") // U+304B + combining dakuten (U+3099)
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
	// APFS/HFS+ treat normalization-only differences as the same name, so the
	// second write overwrites the first and no collision occurs.
	if len(names) < 2 {
		t.Skip("filesystem merges NFC/NFD names; collision cannot occur here")
	}

	err = Create(CreateOptions{Source: src, Dest: filepath.Join(tmp, "out.zip")})
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Errorf("err = %v, want collision error", err)
	}
}

// TestCreate_NFCCollisionBetweenDirsIsError confirms that collisions between
// directories are also detected. seen's key omits the trailing "/", so a
// collision between a directory and a file is caught too.
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
	// APFS/HFS+ treat normalization-only differences as the same name, so the
	// second MkdirAll resolves to the existing directory and no collision occurs.
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

// TestCreate_MacMetadataSourceIsError confirms that specifying a Source whose
// name matches an exclusion rule fails with an error, instead of silently
// producing an empty (or partly missing) archive.
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
	// A link whose name embeds a terminal escape sequence. Confirms the notice doesn't let it hijack the terminal.
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
	// Even for an entry whose name contains ESC, no raw escape sequence that could control the terminal is emitted.
	if strings.Contains(stderr.String(), "\x1b") {
		t.Errorf("stderr contains a raw ESC: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), `\u001b[31m`) {
		t.Errorf("stderr = %q, want the ESC in the name escaped", stderr.String())
	}
}

// TestCreate_WarnsOnBackslashInName confirms that a backslash in a file name
// is warned about. Some extractors treat it as a directory separator
// (zip2win inspect also reports it as a problem).
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
	dst := filepath.Join(src, "out.zip") // destination is inside the input directory

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

// TestCreate_DoesNotIncludeItselfViaPathAlias confirms that even when Source
// and Dest refer to the same underlying file through different aliases (such
// as macOS's /tmp vs. /private/tmp), the in-progress ZIP is never pulled into
// itself. The alias itself is a symlink, but the final component
// alias/docs is not a symlink, so it must still be accepted.
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

	src := filepath.Join(alias, "docs")           // through the alias (alias itself is a symlink)
	dst := filepath.Join(real, "docs", "out.zip") // written to the real path side

	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := []string{"docs/", "docs/a.txt"}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

// TestCreate_ResolvesSourcePathAliases confirms that even when Source is
// given as an alias path through a symlink, the walk operates on the real
// path (this pins down Create's EvalSymlinks(srcAbs) call). Without that
// resolution, notices and errors would point at an apparent path that
// doesn't match the real file, making it impossible to tell which file is
// being referred to. The root name must still be exactly what was given.
func TestCreate_ResolvesSourcePathAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "real")
	mustWrite(t, filepath.Join(realDir, "docs", "a.txt"), "a")
	// A symlink whose sole purpose is to trigger a notice. The path in that notice tells us whether resolution happened.
	if err := os.Symlink(filepath.Join(realDir, "docs", "a.txt"), filepath.Join(realDir, "docs", "link.txt")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(tmp, "alias")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Fatal(err)
	}
	// tmp itself can be reached through a symlink (macOS's /var -> /private/var), so resolve the expected value too.
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

// TestCreate_DoesNotIncludeItselfCaseInsensitive confirms that self-inclusion
// is still prevented on a case-insensitive file system (the default for
// APFS / NTFS) even when Source and Dest differ only in spelling.
// Comparing path strings can't catch this — it needs file-identity
// comparison (os.SameFile).
func TestCreate_DoesNotIncludeItselfCaseInsensitive(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "SRC")
	mustWrite(t, filepath.Join(src, "a.txt"), "a")
	// Confirm this file system actually ignores case before proceeding.
	if _, err := os.Stat(filepath.Join(tmp, "src")); err != nil {
		t.Skip("filesystem is case-sensitive; the alias cannot occur here")
	}
	dst := filepath.Join(tmp, "src", "out.zip") // points at the same directory with a different spelling

	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := []string{"SRC/", "SRC/a.txt"}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

// TestCreate_SingleFileSourceIsDestIsError confirms that for a single-file
// source, when Source and Dest are the same underlying file, the rename
// never replaces (and destroys) the original file with the ZIP.
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

// TestCreate_SourceNamedLikeTempFileIsArchived confirms that a source that
// happens to be named <dest>.tmp is just another input file. Temp files carry
// a random name component, so no user-chosen name is ever treated as "ours"
// and cleaned up.
func TestCreate_SourceNamedLikeTempFileIsArchived(t *testing.T) {
	tmp := t.TempDir()
	dst := filepath.Join(tmp, "out.zip")
	src := dst + ".tmp"
	mustWrite(t, src, "precious")

	if err := Create(CreateOptions{Source: src, Dest: dst, Force: true}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b, err := os.ReadFile(src); err != nil || string(b) != "precious" {
		t.Errorf("source file was removed or modified: %q (err=%v)", b, err)
	}
	entries := readZip(t, dst)
	f, ok := entries["out.zip.tmp"]
	if !ok {
		t.Fatalf("entries = %v, want [out.zip.tmp]", entryNames(entries))
	}
	if got := readEntry(t, f); got != "precious" {
		t.Errorf("content = %q, want precious", got)
	}
}

// assertNoTempFiles fails if any temp file for dst (<dst>.<random>.tmp, or the
// old fixed <dst>.tmp) exists.
func assertNoTempFiles(t *testing.T, dst string) {
	t.Helper()
	matches, err := filepath.Glob(dst + "*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("temporary files left behind: %v", matches)
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

// TestCreate_LeftoverTempFileDoesNotBlock confirms that temp files left behind
// by an interrupted run never block a later run (no --force needed) and are
// never touched: with random names we can't tell ours from the user's.
func TestCreate_LeftoverTempFileDoesNotBlock(t *testing.T) {
	tmp := t.TempDir()
	src := newSourceDir(t, tmp)
	dst := filepath.Join(tmp, "out.zip")
	leftovers := []string{dst + ".tmp", dst + ".AAAAAAAAAAAAAAAAAAAAAAAAAA.tmp"}
	for _, p := range leftovers {
		mustWrite(t, p, "stale")
	}

	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, []string{"docs/", "docs/a.txt"}) {
		t.Errorf("entries = %v, want [docs/ docs/a.txt]", got)
	}
	for _, p := range leftovers {
		if b, err := os.ReadFile(p); err != nil || string(b) != "stale" {
			t.Errorf("%s was removed or modified: %q (err=%v)", p, b, err)
		}
	}
}

// TestCreate_SuccessLeavesNoTempFiles confirms the temp file is renamed into
// place, not copied: nothing matching <dest>.*.tmp remains after success.
func TestCreate_SuccessLeavesNoTempFiles(t *testing.T) {
	tmp := t.TempDir()
	src := newSourceDir(t, tmp)
	dst := filepath.Join(tmp, "out.zip")

	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertNoTempFiles(t, dst)
}

// TestCreate_DoesNotIncludeExistingDestWithForce confirms that when
// overwriting an existing destination under --force, the walk never includes
// the pre-overwrite Dest it encounters along the way.
func TestCreate_DoesNotIncludeExistingDestWithForce(t *testing.T) {
	tmp := t.TempDir()
	src := newSourceDir(t, tmp)
	dst := filepath.Join(src, "out.zip") // destination is inside the input directory
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
	if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s should not exist after failure (err=%v)", dst, err)
	}
	assertNoTempFiles(t, dst)
}
