// zip2win は Windows の標準機能で展開しても文字化けしない ZIP を作成・検査する CLI。
package main

import (
	"fmt"
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

// run はサブコマンドを振り分け、exit code を返す。
// os.Exit を直接呼ばないのはテストから呼べるようにするため。
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	default:
		fmt.Fprintf(stderr, "zip2win: unknown command %q\n", args[0])
		fmt.Fprint(stderr, usageText)
		return 2
	}
}
