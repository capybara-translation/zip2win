//go:build unix

package zipwin

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// TestCreate_SkipsNonRegularFileWithNotice は FIFO のような「通常ファイルでもディレクトリでもない」
// エントリをスキップすることを確認する。開こうとすると読み手が来るまでブロックするため、
// スキップしないと Create が返らなくなる。
// syscall.Mkfifo を使うのでこのファイルは unix 限定。
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
