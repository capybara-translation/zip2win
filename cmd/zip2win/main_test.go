package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
