// Command slop-police lints the TypeScript projects listed in
// ./slop-police.json. It exits with 1 when there are errors and 2 for config
// errors. SLOP_POLICE_TIMING=1 prints phase timings to stderr.
//
// With --api --cwd <dir> it serves the TypeScript API on stdin and stdout
// instead, for the npm launcher that runs plugin rules (see lint.Serve).
package main

import (
	"bufio"
	"flag"
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
	if len(os.Args) > 1 && os.Args[1] == "--api" {
		os.Exit(serve(os.Args[2:], lint.NewTiming(timingOut)))
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

func serve(args []string, timing *lint.Timing) int {
	flags := flag.NewFlagSet("api", flag.ContinueOnError)
	cwd := flags.String("cwd", "", "project root")
	if err := flags.Parse(args); err != nil || *cwd == "" {
		return 2
	}
	if err := lint.Serve(*cwd, os.Stdin, os.Stdout, timing); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
