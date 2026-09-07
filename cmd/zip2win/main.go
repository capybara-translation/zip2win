// zip2win is a CLI that creates and inspects ZIP archives whose file names
// survive extraction with Windows' built-in tools (Explorer) without mojibake.
package main

import (
	"flag"
	"fmt"
	"github.com/capybara-translation/zip2win/internal/zipwin"
	"io"
	"os"
)

const usageText = `Usage:
  zip2win create [--force] <source> <output.zip>
  zip2win inspect <file.zip>

Flags must come before positional arguments.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches to a subcommand and returns the exit code.
// It doesn't call os.Exit directly so tests can call it.
//
// Errors are always passed through zipwin.DisplayName before going to stderr.
// If a file name wrapped in something like *fs.PathError reached the terminal
// unescaped, an escape sequence embedded in the name could hijack the display.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "create":
		return runCreate(args[1:], stdout, stderr)
	case "inspect":
		return runInspect(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "zip2win: unknown command %q\n", args[0])
		fmt.Fprint(stderr, usageText)
		return 2
	}
}

// runCreate runs the create subcommand.
func runCreate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stderr, usageText) }
	force := flags.Bool("force", false, "overwrite the output file if it exists")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	err := zipwin.Create(zipwin.CreateOptions{
		Source: flags.Arg(0),
		Dest:   flags.Arg(1),
		Stderr: stderr,
		Force:  *force,
	})
	if err != nil {
		fmt.Fprintf(stderr, "zip2win: %s\n", zipwin.DisplayName(err.Error()))
		return 1
	}
	return 0
}

// runInspect runs the inspect subcommand. Inspection failures also exit 1.
func runInspect(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	report, err := zipwin.Inspect(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "zip2win: %s\n", zipwin.DisplayName(err.Error()))
		return 1
	}
	if err := report.Format(stdout); err != nil {
		fmt.Fprintf(stderr, "zip2win: %s\n", zipwin.DisplayName(err.Error()))
		return 1
	}
	if !report.OK() {
		fmt.Fprintln(stderr, "zip2win: inspection found problems")
		return 1
	}
	return 0
}
