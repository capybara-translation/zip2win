// Package zipwin creates and inspects ZIP archives whose file names survive
// extraction with Windows' built-in tools without turning into mojibake.
//
// The core of the fix is three things:
//   - store file names as UTF-8
//   - set bit 11 (EFS) of the General Purpose Bit Flag to declare the names as UTF-8
//   - normalize macOS-originated NFD names to NFC
package zipwin

import (
	"archive/zip"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// utf8Flag is bit 11 (EFS / Language Encoding Flag) of the ZIP General Purpose
// Bit Flag. Go's Writer sets it automatically for non-ASCII UTF-8 names, but
// we set it explicitly even for ASCII names so every entry declares it.
const utf8Flag = 1 << 11

// CreateOptions is the input to Create.
type CreateOptions struct {
	// Source is the directory or single file to archive.
	Source string
	// Dest is the path of the ZIP to write.
	Dest string
	// Stderr receives a notice whenever a symbolic link is skipped. If nil, no notice is written.
	Stderr io.Writer
	// Force, if true, overwrites an existing Dest.
	Force bool
}

// Create archives opts.Source into opts.Dest.
// For a directory, that directory's name becomes the archive's root folder.
func Create(opts CreateOptions) error {
	srcAbs, err := filepath.Abs(opts.Source)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}
	dstAbs, err := filepath.Abs(opts.Dest)
	if err != nil {
		return fmt.Errorf("resolve destination path: %w", err)
	}
	// Passing "/" or "." would leave no name to use as the root folder.
	base := filepath.Base(srcAbs)
	if base == "." || base == string(filepath.Separator) {
		return fmt.Errorf("source %q has no name to use as the archive root", opts.Source)
	}
	// If Source itself matches an exclusion rule, the walk silently produces an
	// empty archive, or (as with anything under __MACOSX) one whose contents are
	// entirely dropped. Treat that as a usage mistake.
	if isMacMetadata(base) {
		return fmt.Errorf("source %q is an excluded macOS metadata name", opts.Source)
	}
	// Lstat is deliberate: if Source itself is a symlink, error out instead of
	// following it (the symlink-non-following policy applies to the root too).
	info, err := os.Lstat(srcAbs)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("source %q is a symbolic link", opts.Source)
	}
	// FIFOs, sockets, and device files can't go into a ZIP, and os.Open on a FIFO
	// can block forever waiting for a reader. A single-file source never goes
	// through the walk, so the equivalent skip logic for directory entries
	// doesn't protect it. Reject it here, before any temp file is created.
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("source %q is not a regular file", opts.Source)
	}
	// Resolve the path we walk to its real path (e.g. macOS's /tmp vs. /private/tmp
	// aliasing). The Lstat above already confirmed the final component isn't a
	// symlink, so resolving here doesn't change the archive's root name
	// (filepath.Base).
	srcAbs, err = filepath.EvalSymlinks(srcAbs)
	if err != nil {
		return fmt.Errorf("resolve source symlinks: %w", err)
	}
	// dstAbs may not exist yet, so it can't be resolved directly. Resolve only
	// its parent directory and join back the base name. This doubles as
	// confirming the destination directory exists.
	dstDir, err := filepath.EvalSymlinks(filepath.Dir(dstAbs))
	if err != nil {
		return fmt.Errorf("resolve destination directory: %w", err)
	}
	dstAbs = filepath.Join(dstDir, filepath.Base(dstAbs))

	// For a single-file source, if Source and Dest are the same underlying file,
	// the rename done under --force would replace the original file with the
	// ZIP (destroying the input data).
	if !info.IsDir() {
		if dstInfo, err := os.Lstat(dstAbs); err == nil && os.SameFile(info, dstInfo) {
			return errors.New("source and destination are the same file")
		}
	}

	if err := checkDest(dstAbs, opts.Force); err != nil {
		return err
	}

	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	// Write to a temp file in the same directory, then rename it into place once
	// done. A failure partway through never leaves a broken ZIP at Dest, and a
	// --force overwrite happens as a single atomic swap once the archive is complete.
	out, tmpPath, err := createTemp(dstAbs)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	// Self-inclusion is decided by file identity, not path strings.
	// The temp file's info comes from the already-open fd (so a swap right
	// after opening it can't be mistaken for a different file).
	var excluded []fs.FileInfo
	tmpInfo, err := out.Stat()
	if err != nil {
		_ = out.Close()
		return fmt.Errorf("stat temporary file: %w", err)
	}
	excluded = append(excluded, tmpInfo)
	// Dest only exists when overwriting under --force. If it's a symlink, the
	// rename replaces the link itself, so remember the symlink's own identity,
	// not its target's (the walk likewise never follows symlinks).
	if dstInfo, err := os.Lstat(dstAbs); err == nil {
		excluded = append(excluded, dstInfo)
	}

	zw := zip.NewWriter(out)
	c := &creator{
		zw:       zw,
		base:     filepath.Dir(srcAbs),
		excluded: excluded,
		stderr:   stderr,
		seen:     map[string]string{},
	}
	if err := c.walk(srcAbs, info); err != nil {
		_ = zw.Close()
		_ = out.Close()
		return err
	}
	// Close writes the central directory. Skipping this check would let a
	// broken ZIP be reported as a success.
	if err := zw.Close(); err != nil {
		_ = out.Close()
		return fmt.Errorf("finalize zip: %w", err)
	}
	// fsync before renaming. Without this, a crash could leave behind a ZIP
	// that exists by name but is empty inside (rename is metadata-only and
	// doesn't guarantee the data itself has reached disk).
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(tmpPath, dstAbs); err != nil {
		return fmt.Errorf("move zip into place: %w", err)
	}
	committed = true
	return nil
}

// createTemp creates the temp file the archive is written to, next to dstAbs
// so the final rename stays on one file system, and returns it with its path.
//
// The name carries a random component (<dest>.<random>.tmp). A fixed name can
// collide with a file the user owns, and a leftover from an interrupted run
// would block every later run until someone deletes it; a random name has
// neither problem, so there is never anything of "ours" to clean up.
//
// os.CreateTemp does the same thing but hard-codes mode 0600, which would make
// every archive unreadable to other users. Opening with 0o666 lets the umask
// decide, as for any other file the user creates.
//
// O_EXCL stays even though the name is random: randomness makes a collision
// unlikely, O_EXCL makes one harmless. It also refuses to open a symlink (or
// anything else) that someone else placed at that path.
func createTemp(dstAbs string) (*os.File, string, error) {
	tmpPath := dstAbs + "." + rand.Text() + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return nil, "", fmt.Errorf("create temporary file: %w", err)
	}
	return f, tmpPath, nil
}

// checkDest is the upfront check on the destination: OK if it doesn't exist,
// always an error if it's a directory, and allowed for an existing file only
// when force is set.
func checkDest(dstAbs string, force bool) error {
	// Lstat is deliberate. Even if the destination is a symlink, rename
	// replaces the link itself (never writes through it to its target), so we
	// check for the symlink's own existence and type, not its target's.
	info, err := os.Lstat(dstAbs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat destination: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("destination %q is a directory", dstAbs)
	}
	if !force {
		return fmt.Errorf("destination %q already exists (use --force to overwrite)", dstAbs)
	}
	return nil
}

// creator holds the state for a single ZIP-creation run.
type creator struct {
	zw *zip.Writer
	// base is the directory that ZIP-internal paths are computed relative to.
	// It's Source's parent, which is what makes Source's own name the root.
	base string
	// excluded holds the file identities to skip if encountered during the
	// walk (the output ZIP itself and the temp file). Compared with
	// os.SameFile rather than as path strings, so a spelling difference on a
	// case-insensitive file system or a hard-linked alias is still caught.
	excluded []fs.FileInfo
	// stderr is where skip notices are written.
	stderr io.Writer
	// seen maps a normalized name to the un-normalized relative path that
	// produced it. It detects collisions where NFC normalization makes two
	// different files share a name. Directories are registered too, and the
	// key deliberately omits the trailing "/": a directory and a file with the
	// same name can't coexist after extraction, so that's treated as a collision.
	seen map[string]string
}

// walk adds everything under srcAbs (a directory or a file) in turn.
// Exclusions and symlinks are filtered out here.
func (c *creator) walk(srcAbs string, info fs.FileInfo) error {
	if !info.IsDir() {
		// Run a single-file source through the same exclusion check as
		// directory entries. Skipping it silently here would produce an
		// empty ZIP, so it's an error instead — a second line of defense
		// alongside Create's own upfront check.
		if c.isExcluded(info) {
			return errors.New("source is the output file")
		}
		return c.add(srcAbs, info)
	}
	return filepath.WalkDir(srcAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if isMacMetadata(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// d.Info() doesn't follow symlinks (equivalent to Lstat).
		info, err := d.Info()
		if err != nil {
			return err
		}
		// If the output ZIP sits inside the input directory, this keeps the
		// walk from reading its own still-being-written self.
		if c.isExcluded(info) {
			return nil
		}
		// WalkDir doesn't follow symlinks. To avoid unintentionally pulling in
		// whatever the link points to, or a loop, we neither store it as an
		// entry nor drop it silently — just emit a notice.
		// The notice is passed through DisplayName so an escape sequence
		// embedded in the name never reaches the terminal raw.
		if d.Type()&fs.ModeSymlink != 0 {
			fmt.Fprintf(c.stderr, "zip2win: skipping symbolic link: %s\n", DisplayName(path))
			return nil
		}
		// FIFOs, sockets, and device files can't go into a ZIP, and os.Open on
		// a FIFO can block forever waiting for a reader. Skip them with a
		// notice, the same as symlinks.
		if !info.Mode().IsRegular() && !info.IsDir() {
			fmt.Fprintf(c.stderr, "zip2win: skipping non-regular file: %s\n", DisplayName(path))
			return nil
		}
		return c.add(path, info)
	})
}

// isExcluded reports whether info is a file identity that should not be archived.
func (c *creator) isExcluded(info fs.FileInfo) bool {
	for _, ex := range c.excluded {
		if os.SameFile(info, ex) {
			return true
		}
	}
	return false
}

// isMacMetadata reports whether name is a metadata file or folder name macOS generates:
//   - .DS_Store: Finder's per-folder display settings
//   - __MACOSX: the folder holding AppleDouble sidecar data and similar
//   - ._*: AppleDouble sidecar files
func isMacMetadata(name string) bool {
	return name == ".DS_Store" || name == "__MACOSX" || strings.HasPrefix(name, "._")
}

// entryName converts path into rel, its path relative to base, and name, the
// ZIP-internal form: rel with separators normalized to / and NFC-normalized.
func (c *creator) entryName(path string) (rel, name string, err error) {
	rel, err = filepath.Rel(c.base, path)
	if err != nil {
		return "", "", err
	}
	// On Linux a file name can be an arbitrary byte sequence. Since we set
	// EFS, invalid UTF-8 is rejected outright.
	if !utf8.ValidString(rel) {
		return "", "", fmt.Errorf("file name is not valid UTF-8: %q", rel)
	}
	return rel, norm.NFC.String(filepath.ToSlash(rel)), nil
}

// add writes a single entry to the ZIP.
func (c *creator) add(path string, info fs.FileInfo) error {
	rel, name, err := c.entryName(path)
	if err != nil {
		return err
	}
	// A ZIP with more than one entry at the same path can't have both
	// extracted, and different extractors handle that inconsistently.
	if prev, dup := c.seen[name]; dup {
		return fmt.Errorf("entry name collision after NFC normalization: %q and %q both become %q", prev, rel, name)
	}
	c.seen[name] = rel
	// Some extractors treat a backslash as a directory separator, which can
	// produce an unintended hierarchy or serve as path-traversal material
	// (inspect also reports this as a problem). Since rewriting the name
	// can't be undone, we only warn and continue.
	if strings.Contains(name, `\`) {
		fmt.Fprintf(c.stderr, "zip2win: warning: name contains backslash, some extractors treat it as a separator: %s\n", DisplayName(name))
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return fmt.Errorf("build header for %q: %w", path, err)
	}
	header.Name = name
	header.NonUTF8 = false
	header.Flags |= utf8Flag

	if info.IsDir() {
		// A trailing / marks a directory entry. The Writer stores it with
		// size 0 using Store.
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
