package zipwin

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// writeRawZip builds a ZIP with arbitrary headers, for inspection tests.
// Since the Writer doesn't validate names, this can also build invalid ZIPs
// such as ones containing "../" or duplicate names.
func writeRawZip(t *testing.T, path string, headers []*zip.FileHeader) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, h := range headers {
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(h.Name, "/") {
			if _, err := w.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func findEntry(t *testing.T, r *Report, name string) Entry {
	t.Helper()
	for _, e := range r.Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("entry %q not found in %+v", name, r.Entries)
	return Entry{}
}

func hasProblem(e Entry, substr string) bool {
	return slices.ContainsFunc(e.Problems, func(p string) bool { return strings.Contains(p, substr) })
}

func TestInspect_ZipFromCreateIsOK(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "docs")
	mustWrite(t, filepath.Join(src, "日本語.txt"), "x")
	mustWrite(t, filepath.Join(src, "ascii.txt"), "x")
	dst := filepath.Join(tmp, "out.zip")
	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatal(err)
	}

	r, err := Inspect(dst)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !r.OK() {
		t.Errorf("report not OK: %+v", r.Entries)
	}
	if len(r.Entries) != 3 {
		t.Errorf("got %d entries, want 3", len(r.Entries))
	}
}

func TestInspect_DetectsProblems(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.zip")
	writeRawZip(t, path, []*zip.FileHeader{
		{Name: "日本語.txt", NonUTF8: true}, // no EFS
		{Name: "../evil.txt"},            // path traversal
		{Name: "/abs.txt"},               // absolute path
		{Name: `dir\file.txt`},           // backslash
		{Name: ".DS_Store"},              // Mac metadata
		{Name: "sub/._x"},                // Mac metadata (in a subdirectory)
		{Name: "dup.txt"},
		{Name: "dup.txt"}, // duplicate
		{Name: "fine.txt", Flags: utf8Flag},
	})

	r, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if r.OK() {
		t.Fatal("report should not be OK")
	}
	cases := []struct{ name, problem string }{
		{"日本語.txt", "EFS"},
		{"../evil.txt", "traversal"},
		{"/abs.txt", "absolute"},
		{`dir\file.txt`, "backslash"},
		{".DS_Store", "macOS"},
		{"sub/._x", "macOS"},
	}
	for _, c := range cases {
		if e := findEntry(t, r, c.name); !hasProblem(e, c.problem) {
			t.Errorf("%q: problems = %v, want one containing %q", c.name, e.Problems, c.problem)
		}
	}
	dups := 0
	for _, e := range r.Entries {
		// A byte-for-byte duplicate name gets a message distinguishable from a normalization-only duplicate.
		if e.Name == "dup.txt" && slices.Contains(e.Problems, "duplicate entry name") {
			dups++
		}
	}
	if dups != 1 {
		t.Errorf("duplicate flagged %d times, want exactly 1 (the second occurrence)", dups)
	}
	if e := findEntry(t, r, "fine.txt"); len(e.Problems) != 0 {
		t.Errorf("fine.txt should have no problems, got %v", e.Problems)
	}
}

// TestInspect_FlagsNonNFCName confirms that an entry name left in NFD is
// reported. Windows displays and compares names assuming NFC, so an NFD name
// can show its combining characters split apart.
func TestInspect_FlagsNonNFCName(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "nfd.zip")
	nfd := norm.NFD.String("が.txt") // U+304B + combining dakuten (U+3099)
	writeRawZip(t, path, []*zip.FileHeader{{Name: nfd, Flags: utf8Flag}})

	r, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if e := findEntry(t, r, nfd); !hasProblem(e, "NFC") {
		t.Errorf("problems = %v, want a not-NFC problem", e.Problems)
	}
}

// TestInspect_FlagsDuplicateAfterNFC confirms that two entries that become
// the same name once NFC-normalized are reported as duplicates. Extracting
// them on Windows would write both to the same path.
func TestInspect_FlagsDuplicateAfterNFC(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "dup.zip")
	nfc := "が.txt"
	nfd := norm.NFD.String(nfc)
	writeRawZip(t, path, []*zip.FileHeader{
		{Name: nfc, Flags: utf8Flag},
		{Name: nfd, Flags: utf8Flag},
	})

	r, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if e := findEntry(t, r, nfc); len(e.Problems) != 0 {
		t.Errorf("first (NFC) entry: problems = %v, want none", e.Problems)
	}
	if e := findEntry(t, r, nfd); !hasProblem(e, "duplicate entry name after NFC normalization") {
		t.Errorf("second (NFD) entry: problems = %v, want duplicate-after-NFC", e.Problems)
	}
}

func TestInspect_ASCIIWithoutEFSIsFlagged(t *testing.T) {
	// The policy requires EFS on every entry, so even an ASCII name without EFS is flagged.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ascii.zip")
	writeRawZip(t, path, []*zip.FileHeader{{Name: "plain.txt"}})

	r, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if e := findEntry(t, r, "plain.txt"); !hasProblem(e, "EFS") {
		t.Errorf("problems = %v, want EFS problem", e.Problems)
	}
}

// TestInspect_DriveLetterOnlyForASCIILetter confirms that a ':' as the second
// character alone doesn't get treated as an absolute path. A drive letter is
// limited to an ASCII letter.
func TestInspect_DriveLetterOnlyForASCIILetter(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "drive.zip")
	writeRawZip(t, path, []*zip.FileHeader{
		{Name: "C:/x", Flags: utf8Flag},
		{Name: "1:2.txt", Flags: utf8Flag},
	})

	r, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if e := findEntry(t, r, "C:/x"); !hasProblem(e, "absolute") {
		t.Errorf("C:/x: problems = %v, want absolute path", e.Problems)
	}
	if e := findEntry(t, r, "1:2.txt"); hasProblem(e, "absolute") {
		t.Errorf("1:2.txt: problems = %v, want no absolute path problem", e.Problems)
	}
}

// TestCreateThenInspect_IsOK is a round-trip invariant test: inspect must
// always judge a ZIP created by create to be OK. If the two ever drift apart, this catches it.
func TestCreateThenInspect_IsOK(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "配布 資料")
	mustWrite(t, filepath.Join(src, "readme.txt"), "ascii")
	mustWrite(t, filepath.Join(src, "日本語 名前.txt"), "japanese with space")
	mustWrite(t, filepath.Join(src, norm.NFD.String("が.txt")), "nfd on disk")
	mustWrite(t, filepath.Join(src, "sub", "深い階層", "file.txt"), "nested")
	if err := os.MkdirAll(filepath.Join(src, "空フォルダ"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "out.zip")
	if err := Create(CreateOptions{Source: src, Dest: dst}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	r, err := Inspect(dst)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !r.OK() {
		for _, e := range r.Entries {
			if len(e.Problems) > 0 {
				t.Errorf("%q: %v", e.Name, e.Problems)
			}
		}
	}
	// root, readme.txt, the Japanese-named-with-space file, the NFD-normalized file,
	// sub/, the nested deep-hierarchy directory under it, the file.txt inside that, and the empty folder
	if len(r.Entries) != 8 {
		t.Errorf("got %d entries, want 8: %+v", len(r.Entries), r.Entries)
	}
}

func TestInspect_NotAZip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "x.zip")
	mustWrite(t, path, "not a zip")
	if _, err := Inspect(path); err == nil {
		t.Fatal("expected error for non-zip input")
	}
}

// TestReport_FormatAlignsWideNames confirms the columns after the name line up
// when names contain East Asian wide characters. Each takes three bytes but
// two terminal columns, so padding by bytes (%-40s) misaligned the rest of the
// line for exactly the names this tool exists for.
func TestReport_FormatAlignsWideNames(t *testing.T) {
	r := &Report{Entries: []Entry{
		{Name: "docs/readme.txt", Flags: utf8Flag},
		{Name: "資料/サブ/が.txt", Flags: utf8Flag},
		{Name: "ｶﾀｶﾅ/é.txt", Flags: utf8Flag},
	}}
	var buf bytes.Buffer
	if err := r.Format(&buf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	var cols []int
	for _, line := range lines {
		i := strings.Index(line, "EFS=")
		if i < 0 {
			t.Fatalf("no EFS column in %q", line)
		}
		cols = append(cols, displayWidth(line[:i]))
	}
	for i, c := range cols {
		if c != cols[0] {
			t.Errorf("line %d: EFS column starts at %d, want %d (lines %q)", i, c, cols[0], lines)
		}
	}
}

func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"資料", 4},
		{"が", 2},
		{"か\u3099", 2}, // NFD: the combining mark takes no column of its own
		{"ｶﾀｶﾅ", 4},    // halfwidth katakana
		{"é", 1},
		{"", 0},
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestInspect_FlagsInvalidUTF8Name confirms that a name which isn't valid
// UTF-8 is reported even though its EFS flag claims it is, and that the bad
// byte is escaped rather than written raw when displayed.
func TestInspect_FlagsInvalidUTF8Name(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.zip")
	writeRawZip(t, path, []*zip.FileHeader{{Name: "bad\xffname.txt", Flags: utf8Flag}})

	r, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(t, r, "bad\xffname.txt")
	if !hasProblem(e, "not valid UTF-8") {
		t.Errorf("problems = %v, want an invalid UTF-8 problem", e.Problems)
	}
	var buf bytes.Buffer
	if err := r.Format(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `bad\xffname.txt`) || strings.Contains(buf.String(), "\xff") {
		t.Errorf("output = %q, want the invalid byte escaped as \\xff", buf.String())
	}
}

func TestReport_FormatEscapesControlChars(t *testing.T) {
	r := &Report{Entries: []Entry{{Name: "a\x1b[31mb.txt", Flags: utf8Flag}}}
	var buf bytes.Buffer
	if err := r.Format(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("output contains raw ESC: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "\\u001b[") {
		t.Errorf("output = %q, want escaped \\u001b", buf.String())
	}
}

func TestDisplayName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"日本語.txt", "日本語.txt"},
		{"a\x1bb", "a\\u001bb"},
		{"bad\xffbyte", `bad\xffbyte`},
		// Bidi controls are invisible but reorder the text around them in a
		// bidi-aware viewer, so they're escaped like other control characters.
		{"invoice_\u202Efdp.exe", `invoice_\u202efdp.exe`},
		{"a\u2066b\u2069", `a\u2066b\u2069`},
		{"a\u200Fb", `a\u200fb`},
	}
	for _, c := range cases {
		if got := DisplayName(c.in); got != c.want {
			t.Errorf("DisplayName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestInspect_FlagsBidiControlCharacters confirms that a name containing a
// Unicode bidirectional control character is reported. "invoice_" + U+202E +
// "fdp.exe" displays as "invoice_exe.pdf" wherever the bidi algorithm is
// applied, disguising an executable as a document.
func TestInspect_FlagsBidiControlCharacters(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bidi.zip")
	writeRawZip(t, path, []*zip.FileHeader{
		{Name: "invoice_\u202Efdp.exe", Flags: utf8Flag},
		{Name: "plain.txt", Flags: utf8Flag},
	})

	r, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if e := findEntry(t, r, "invoice_\u202Efdp.exe"); !hasProblem(e, "bidirectional") {
		t.Errorf("problems = %v, want a bidirectional control character problem", e.Problems)
	}
	if e := findEntry(t, r, "plain.txt"); len(e.Problems) != 0 {
		t.Errorf("plain.txt should have no problems, got %v", e.Problems)
	}
}
