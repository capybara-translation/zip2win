//go:build unix

package zipwin

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// TestCreate_NonRegularSourceIsError confirms that when Source itself is a
// non-regular file such as a FIFO, Create errors out before opening it would
// block (a single-file source never goes through the walk, so the directory
// skip logic can't protect it here). An implementation that fails to return
// would time out this test.
func TestCreate_NonRegularSourceIsError(t *testing.T) {
	tmp := t.TempDir()
	fifo := filepath.Join(tmp, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo is not usable on this filesystem: %v", err)
	}
	dst := filepath.Join(tmp, "out.zip")

	err := Create(CreateOptions{Source: fifo, Dest: dst})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("err = %v, want 'not a regular file' error", err)
	}
	// This must be rejected before a temp file is created (not left to cleanup afterward).
	for _, p := range []string{dst, dst + ".tmp"} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s should not exist (err=%v)", p, err)
		}
	}
}

// TestCreate_SkipsNonRegularFileWithNotice confirms that an entry that is
// "neither a regular file nor a directory", such as a FIFO, gets skipped.
// Opening it would block until a reader shows up, so without the skip Create
// would never return.
// This file is unix-only because it uses syscall.Mkfifo.
func TestCreate_SkipsNonRegularFileWithNotice(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, "real.txt"), "r")
	fifo := filepath.Join(src, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo is not usable on this filesystem: %v", err)
	}
	dst := filepath.Join(tmp, "out.zip")
	var stderr bytes.Buffer

	if err := Create(CreateOptions{Source: src, Dest: dst, Stderr: &stderr}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := []string{"docs/", "docs/real.txt"}
	if got := entryNames(readZip(t, dst)); !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if !strings.Contains(stderr.String(), "skipping non-regular file") || !strings.Contains(stderr.String(), "pipe") {
		t.Errorf("stderr = %q, want non-regular file notice naming pipe", stderr.String())
	}
}
