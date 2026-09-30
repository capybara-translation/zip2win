package zipwin

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

// Entry is the inspection result for one entry in the ZIP.
type Entry struct {
	Name  string
	Flags uint16
	// Problems lists the problems found. Empty means no problems.
	Problems []string
}

// Report is the result of Inspect.
type Report struct {
	Entries []Entry
}

// OK reports whether every entry is free of problems.
func (r *Report) OK() bool {
	for _, e := range r.Entries {
		if len(e.Problems) > 0 {
			return false
		}
	}
	return true
}

// Inspect reads path's ZIP central directory and checks each entry. It never extracts anything.
func Inspect(path string) (*Report, error) {
	zr, err := zip.OpenReader(path)
	// With GODEBUG=zipinsecurepath=0, a ZIP containing ".." or an absolute path
	// is returned along with ErrInsecurePath. The reader itself is still
	// usable, so as an inspection tool we read it anyway and report the problem.
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	report := &Report{Entries: make([]Entry, 0, len(zr.File))}
	// Duplicate detection keys on the NFC-normalized name. The value is the
	// un-normalized name of the first occurrence, kept so a byte-for-byte
	// duplicate can be distinguished from one that only collides after
	// normalization. Windows treats names as NFC, so the latter also ends up
	// written to the same path on extraction.
	seen := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		e := Entry{Name: f.Name, Flags: f.Flags, Problems: checkName(f.Name, f.Flags)}
		key := norm.NFC.String(f.Name)
		if prev, dup := seen[key]; dup {
			if prev == f.Name {
				e.Problems = append(e.Problems, "duplicate entry name")
			} else {
				e.Problems = append(e.Problems, "duplicate entry name after NFC normalization")
			}
		} else {
			seen[key] = f.Name
		}
		report.Entries = append(report.Entries, e)
	}
	return report, nil
}

// checkName lists the problems found with name and flags.
func checkName(name string, flags uint16) []string {
	var problems []string
	if flags&utf8Flag == 0 {
		problems = append(problems, "EFS flag (bit 11) not set")
	}
	if !utf8.ValidString(name) {
		problems = append(problems, "name is not valid UTF-8")
	} else if norm.NFC.String(name) != name {
		// A name left in NFD (as macOS produces) can show its combining
		// characters split apart on Windows, and can also collide with an
		// NFC entry of the same name. Skipped for invalid UTF-8, since the
		// normalization result can't be trusted there.
		problems = append(problems, "name is not NFC-normalized")
	}
	if hasBidiControl(name) {
		problems = append(problems, "bidirectional control character in name")
	}
	// The following are names that can serve as path-traversal material on the extracting side.
	if strings.Contains(name, `\`) {
		problems = append(problems, "backslash in name")
	}
	// A drive letter is limited to a single ASCII letter. Looking at ':' alone
	// would misclassify a merely colon-containing name like "1:2.txt" as an
	// absolute path too.
	if strings.HasPrefix(name, "/") || (len(name) >= 2 && name[1] == ':' && isASCIILetter(name[0])) {
		problems = append(problems, "absolute path")
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			problems = append(problems, "path traversal (..)")
			break
		}
	}
	for _, seg := range strings.Split(name, "/") {
		if isMacMetadata(seg) {
			problems = append(problems, "macOS metadata file")
			break
		}
	}
	return problems
}

// hasBidiControl reports whether name contains a Unicode bidirectional control
// character. They're invisible, but any viewer that applies the bidi algorithm
// reorders the text around them: "invoice_" + U+202E + "fdp.exe" displays as
// "invoice_exe.pdf", disguising an executable as a document.
func hasBidiControl(name string) bool {
	return strings.IndexFunc(name, isBidiControl) >= 0
}

func isBidiControl(r rune) bool {
	return unicode.Is(unicode.Bidi_Control, r)
}

// isASCIILetter reports whether b is a character usable as a Windows drive letter.
func isASCIILetter(b byte) bool {
	return ('A' <= b && b <= 'Z') || ('a' <= b && b <= 'z')
}

// nameColumnWidth is the number of terminal columns the name column is padded
// to in Format. Longer names push the rest of their line to the right.
const nameColumnWidth = 40

// Format writes the result to w in a human-readable form.
func (r *Report) Format(w io.Writer) error {
	for _, e := range r.Entries {
		status := "ok"
		if len(e.Problems) > 0 {
			status = "NG: " + strings.Join(e.Problems, "; ")
		}
		efs := e.Flags&utf8Flag != 0
		if _, err := fmt.Fprintf(w, "%s EFS=%-5v Flags=%#04x %s\n", padRight(DisplayName(e.Name), nameColumnWidth), efs, e.Flags, status); err != nil {
			return err
		}
	}
	return nil
}

// padRight pads s with spaces to w terminal columns. fmt's %-40s pads by bytes,
// which misaligns names with East Asian characters: three bytes each, but two
// columns wide.
func padRight(s string, w int) string {
	if n := w - displayWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// displayWidth returns the number of terminal columns s occupies: two for East
// Asian wide and fullwidth characters, none for combining marks and other
// invisible format characters, one for everything else. Ambiguous-width
// characters count as one, as most terminals outside CJK locales render them;
// there's no way to know how a given terminal is configured.
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		case isWide(r):
			n += 2
		default:
			n++
		}
	}
	return n
}

func isWide(r rune) bool {
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return true
	}
	return false
}

// DisplayName escapes control characters and invalid bytes so name can be
// shown on a terminal safely. This keeps a malicious ZIP's entry name (or a
// file name) from hijacking the terminal even if it embeds an escape sequence.
// It's meant to be applied not just to entry names but to any error message
// that might carry a file name (such as one wrapped in *fs.PathError).
// Newlines are escaped as control characters too, so one-message-per-line
// output can't be broken by an embedded newline.
// Bidirectional control characters are escaped as well, so a name can't be
// visually reordered to pass for a different one.
func DisplayName(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, name[i])
		case unicode.IsControl(r) || isBidiControl(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}
