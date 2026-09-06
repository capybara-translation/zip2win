// zip2win は Windows の標準機能で展開しても文字化けしない ZIP を作成・検査する CLI。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"zip2win/internal/zipwin"
)

const usageText = `Usage:
  zip2win create [--force] <source> <output.zip>
  zip2win inspect <file.zip>

Flags must come before positional arguments.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run はサブコマンドを振り分け、exit code を返す。
// os.Exit を直接呼ばないのはテストから呼べるようにするため。
//
// エラーは必ず zipwin.DisplayName を通してから stderr へ出す。
// *fs.PathError などに包まれたファイル名がそのまま端末へ流れると、
// 名前に仕込まれたエスケープシーケンスで表示を操作されうる。
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

// runCreate は create サブコマンドを実行する。
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

// runInspect は inspect サブコマンドを実行する。検査 NG も exit 1。
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
