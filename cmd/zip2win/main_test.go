package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRun_EscapesControlCharactersInErrorOutput confirms that the error-output
// path also never lets a terminal escape through. Errors carry file names
// wrapped in things like *fs.PathError, so escaping only zipwin's own notices
// would still let a raw ESC reach stderr.
func TestRun_EscapesControlCharactersInErrorOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0 does not block reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read files regardless of mode")
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(src, "bad\x1b[31m.txt")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bad, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(bad, 0o644) })

	var stdout, stderr bytes.Buffer
	if code := run([]string{"create", src, filepath.Join(tmp, "out.zip")}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "\x1b") {
		t.Errorf("stderr contains a raw ESC: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), `\u001b`) {
		t.Errorf("stderr = %q, want the ESC in the file name escaped", stderr.String())
	}
}

func TestRun_NoArgsShowsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr = %q, want usage text", stderr.String())
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("stderr = %q, want 'unknown command'", stderr.String())
	}
}

func TestRun_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Errorf("stdout = %q, want usage text", stdout.String())
	}
}

func TestRun_CreateProducesZip(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "out.zip")

	var stdout, stderr bytes.Buffer
	code := run([]string{"create", src, dst}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr.String())
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("output zip not created: %v", err)
	}
}

func TestRun_CreateWrongArgCount(t *testing.T) {
	for _, args := range [][]string{{"create"}, {"create", "a", "b", "c"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("%v: exit code = %d, want 2", args, code)
		}
		want := fmt.Sprintf("zip2win: create takes 1 or 2 arguments, got %d", len(args)-1)
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("%v: stderr = %q, want it to contain %q", args, stderr.String(), want)
		}
	}
}

// makeTree creates work/docs/a.txt under a fresh temp dir and returns the temp dir.
func makeTree(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "work", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "work", "docs", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	return tmp
}

// zipEntries returns the entry names of the ZIP at path.
func zipEntries(t *testing.T, path string) []string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer r.Close()
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names
}

// TestRun_CreateDefaultOutput confirms that without an output path the ZIP is
// written next to the source (in its parent directory, not the current one) as
// <source>.zip. For a file, ".zip" is appended rather than replacing the
// extension, so report.txt and report.pdf can't both map to report.zip.
func TestRun_CreateDefaultOutput(t *testing.T) {
	tests := []struct {
		name   string
		cwd    string // relative to the temp dir
		source string // as typed on the command line, relative to cwd
		want   string // relative to the temp dir
	}{
		{"relative directory", "work", "docs", "work/docs.zip"},
		{"trailing slash", "work", "docs/", "work/docs.zip"},
		{"path from elsewhere lands next to the source", ".", "work/docs", "work/docs.zip"},
		{"current directory lands in its parent", "work/docs", ".", "work/docs.zip"},
		{"single file keeps its extension", "work/docs", "a.txt", "work/docs/a.txt.zip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := makeTree(t)
			t.Chdir(filepath.Join(tmp, tt.cwd))

			var stdout, stderr bytes.Buffer
			if code := run([]string{"create", tt.source}, &stdout, &stderr); code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
			}
			want := filepath.Join(tmp, filepath.FromSlash(tt.want))
			if len(zipEntries(t, want)) == 0 {
				t.Errorf("%s has no entries", want)
			}
		})
	}
}

func TestRun_CreateDefaultOutputExistsWithoutForce(t *testing.T) {
	tmp := makeTree(t)
	t.Chdir(filepath.Join(tmp, "work"))
	if err := os.WriteFile("docs.zip", []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"create", "docs"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Errorf("stderr = %q, want an 'already exists' error", stderr.String())
	}
	if b, _ := os.ReadFile("docs.zip"); string(b) != "precious" {
		t.Errorf("existing docs.zip was modified: %q", b)
	}

	stderr.Reset()
	if code := run([]string{"create", "--force", "docs"}, &stdout, &stderr); code != 0 {
		t.Fatalf("with --force: exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got := zipEntries(t, "docs.zip"); len(got) != 2 {
		t.Errorf("entries = %v, want [docs/ docs/a.txt]", got)
	}
}

func TestRun_CreateMissingSourceIsRuntimeError(t *testing.T) {
	tmp := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"create", filepath.Join(tmp, "nope"), filepath.Join(tmp, "out.zip")}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "zip2win:") {
		t.Errorf("stderr = %q, want error prefixed with zip2win:", stderr.String())
	}
}

func TestRun_CreateForceFlag(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "out.zip")
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"create", src, dst}, &stdout, &stderr); code != 1 {
		t.Errorf("without --force: exit code = %d, want 1", code)
	}
	stderr.Reset()
	if code := run([]string{"create", "--force", src, dst}, &stdout, &stderr); code != 0 {
		t.Errorf("with --force: exit code = %d, want 0; stderr = %s", code, stderr.String())
	}
}

func TestRun_InspectGoodZipReturnsZero(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "日本語.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "out.zip")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"create", src, dst}, &stdout, &stderr); code != 0 {
		t.Fatalf("create failed: %s", stderr.String())
	}

	stdout.Reset()
	code := run([]string{"inspect", dst}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "docs/日本語.txt") || !strings.Contains(stdout.String(), "EFS=true") {
		t.Errorf("stdout = %q, want entry listing with EFS=true", stdout.String())
	}
}

func TestRun_InspectBadZipReturnsOne(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "日本語.txt", NonUTF8: true})
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("x"))
	zw.Close()
	f.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"inspect", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "NG") {
		t.Errorf("stdout = %q, want NG marker", stdout.String())
	}
	if !strings.Contains(stderr.String(), "inspection found problems") {
		t.Errorf("stderr = %q, want the summary line on stderr", stderr.String())
	}
}

// TestRun_InspectMissingFileIsRuntimeError checks the read-failure path, as
// opposed to a failed inspection.
func TestRun_InspectMissingFileIsRuntimeError(t *testing.T) {
	tmp := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"inspect", filepath.Join(tmp, "nope.zip")}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "zip2win:") {
		t.Errorf("stderr = %q, want error prefixed with zip2win:", stderr.String())
	}
}

func TestRun_InspectWrongArgCount(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"inspect"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "zip2win: inspect takes 1 argument, got 0") {
		t.Errorf("stderr = %q, want the expected and actual argument counts", stderr.String())
	}
}

// TestRun_FlagAfterArgumentsIsExplained confirms the hint for the most likely
// cause of a wrong argument count: the flag package stops parsing flags at the
// first positional argument, so a trailing --force is taken as a third argument.
func TestRun_FlagAfterArgumentsIsExplained(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"create", "src", "out.zip", "--force"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	want := `"--force" looks like a flag; flags must come before positional arguments`
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}

// TestRun_TrailingFlagIsNotTakenAsOutput confirms that a flag written after the
// source is rejected instead of being used as the output path. The flag package
// stops at the first positional argument, so `create docs --force` used to
// succeed and write a ZIP named "--force". Every spelling the flag package
// accepts for a defined flag, plus -h/--help, counts.
func TestRun_TrailingFlagIsNotTakenAsOutput(t *testing.T) {
	for _, flagArg := range []string{"--force", "-force", "--force=true", "--help", "-h"} {
		t.Run(flagArg, func(t *testing.T) {
			tmp := t.TempDir()
			if err := os.MkdirAll(filepath.Join(tmp, "docs"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(tmp)

			var stdout, stderr bytes.Buffer
			if code := run([]string{"create", "docs", flagArg}, &stdout, &stderr); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			want := fmt.Sprintf("%q looks like a flag; flags must come before positional arguments", flagArg)
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
			}
			if _, err := os.Lstat(filepath.Join(tmp, flagArg)); err == nil {
				t.Errorf("a file named %q was created", flagArg)
			}
		})
	}
}

// TestRun_DashPrefixedNameThatIsNotAFlagIsAccepted pins the other side of the
// rule: only names of defined flags are rejected, so a file name that merely
// starts with "-" can still be given as is.
func TestRun_DashPrefixedNameThatIsNotAFlagIsAccepted(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tmp)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"create", "docs", "-out.zip"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(tmp, "-out.zip")); err != nil {
		t.Errorf("-out.zip not created: %v", err)
	}
}

func TestRun_WrongArgCountWithoutFlagHasNoHint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	run([]string{"create", "a", "b", "c"}, &stdout, &stderr)
	if strings.Contains(stderr.String(), "looks like a flag") {
		t.Errorf("stderr = %q, want no flag hint when no argument looks like a flag", stderr.String())
	}
}

// TestRun_SubcommandHelp confirms that -h/--help on a subcommand behaves like
// the top-level "help": usage on stdout, exit 0. Before inspect had a FlagSet,
// "inspect --help" was taken as a file name and failed with exit 1.
func TestRun_SubcommandHelp(t *testing.T) {
	for _, args := range [][]string{
		{"inspect", "--help"},
		{"inspect", "-h"},
		{"create", "--help"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Errorf("%v: exit code = %d, want 0; stderr = %q", args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Usage:") {
			t.Errorf("%v: stdout = %q, want usage text", args, stdout.String())
		}
	}
}

func TestRun_InspectUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"inspect", "-x", "a.zip"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "zip2win: flag provided but not defined: -x") {
		t.Errorf("stderr = %q, want the flag error prefixed with zip2win:", stderr.String())
	}
}

func TestRun_InspectTooManyArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"inspect", "a.zip", "b.zip"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

// TestRun_FlagErrorIsEscaped confirms that the flag package's own error
// message, which echoes the unknown flag name verbatim, goes through the same
// escaping as every other error.
func TestRun_FlagErrorIsEscaped(t *testing.T) {
	for _, cmd := range []string{"create", "inspect"} {
		var stdout, stderr bytes.Buffer
		run([]string{cmd, "-bad\x1b[31mflag", "a", "b"}, &stdout, &stderr)
		if strings.Contains(stderr.String(), "\x1b") {
			t.Errorf("%s: stderr contains a raw ESC: %q", cmd, stderr.String())
		}
	}
}
