package main

import (
	"fmt"
	"io"
	"os"
)

func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
		fmt.Fprintln(out, "Augustin — harness-first coding agent\nUsage: augustin --help")
		return 0
	}
	fmt.Fprintln(errOut, "unsupported arguments")
	return 2
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
