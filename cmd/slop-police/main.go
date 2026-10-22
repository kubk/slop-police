// Command slop-police lints the TypeScript projects listed in
// ./slop-police.json. It exits with 1 when there are errors and 2 for config
// errors. SLOP_POLICE_TIMING=1 prints phase timings to stderr.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/microsoft/typescript-go/x/slop-police/lint"
	_ "github.com/microsoft/typescript-go/x/slop-police/rules"
)

func main() {
	tuneRuntime()
	var timingOut io.Writer
	if os.Getenv("SLOP_POLICE_TIMING") == "1" {
		timingOut = os.Stderr
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stdout := bufio.NewWriter(os.Stdout)
	code := lint.Run(root, stdout, os.Stderr, lint.NewTiming(timingOut))
	stdout.Flush()
	os.Exit(code)
}
