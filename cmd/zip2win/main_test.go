package main

import (
	"archive/zip"
	"bytes"
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
	var stdout, stderr bytes.Buffer
	code := run([]string{"create", "only-one"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
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
}
