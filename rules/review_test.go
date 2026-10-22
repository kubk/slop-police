package rules_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// Each testdata/review/<name> directory is a project with a slop-police.json
// and an expected.txt holding "exit <code>" followed by the output of the
// JavaScript slop-police for it.
func TestReviewFixtures(t *testing.T) {
	runReviewFixtures(t)
}

// Uses several checkers even for tiny projects, so that results are checked
// not to depend on how files and queries are spread over checkers.
func TestReviewFixturesManyCheckers(t *testing.T) {
	workers, filesPerChecker := lint.Workers, lint.FilesPerChecker
	lint.Workers, lint.FilesPerChecker = 3, 1
	t.Cleanup(func() { lint.Workers, lint.FilesPerChecker = workers, filesPerChecker })
	runReviewFixtures(t)
}

func runReviewFixtures(t *testing.T) {
	dirs, err := filepath.Glob("testdata/review/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no review fixtures: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			expected, err := os.ReadFile(filepath.Join(dir, "expected.txt"))
			if err != nil {
				t.Fatal(err)
			}
			wantExit, wantOutput, _ := strings.Cut(string(expected), "\n")
			root, err := filepath.Abs(dir)
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			gotExit := fmt.Sprintf("exit %d", lint.Run(root, &stdout, &stderr, nil))
			if gotExit != wantExit || !slices.Equal(outputLines(stdout.String()), outputLines(wantOutput)) {
				t.Errorf("got %s\n%s%s\nwant %s\n%s", gotExit, stdout.String(), stderr.String(), wantExit, wantOutput)
			}
		})
	}
}

// outputLines ignores the order of findings and the column padding.
func outputLines(output string) []string {
	var lines []string
	for line := range strings.Lines(output) {
		if fields := strings.Fields(line); len(fields) > 0 {
			lines = append(lines, strings.Join(fields, " "))
		}
	}
	slices.Sort(lines)
	return lines
}
