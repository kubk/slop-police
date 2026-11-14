package lint

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/pprof"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

type Result struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	File     string   `json:"file"`
	Line     int      `json:"line"`   // 1-based
	Column   int      `json:"column"` // 1-based, in UTF-16 code units
	Message  string   `json:"message"`
}

// Run lints the project in root (reading root/slop-police.json), prints the
// report to stdout and returns the exit code: 0, 1 when there are errors, 2
// for config errors and 3 when slop-police crashes (both printed to stderr).
func Run(root string, stdout, stderr io.Writer, timing *Timing) int {
	config, err := LoadConfig(root, nil)
	var results []Result
	if err == nil {
		results, err = Lint(root, config, timing)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return errorExitCode(err)
	}
	fmt.Fprintln(stdout, Format(results))
	return exitCode(results)
}

// exitCode is 1 when there are errors, 0 otherwise.
func exitCode(results []Result) int {
	for _, r := range results {
		if r.Severity == "error" {
			return 1
		}
	}
	return 0
}

// errorExitCode is 2 for config errors, 3 for crashes, 1 for other errors.
func errorExitCode(err error) int {
	switch {
	case errors.As(err, new(*ConfigError)):
		return 2
	case errors.As(err, new(*CrashError)):
		return 3
	}
	return 1
}

// Lint runs every enabled rule and returns the sorted findings that are not
// suppressed by ignore comments.
func Lint(root string, config *Config, timing *Timing) (results []Result, err error) {
	defer recoverCrash(&err)
	requests, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, err := newContext(requests, newProjectSession(root), config, timing)
	if err != nil {
		return nil, err
	}
	perRule, err := runRules(ctx, config.Rules)
	if err != nil {
		return nil, err
	}
	return collectResults(ctx, config, perRule), nil
}

// runRules runs the built-in rules among rules and returns the findings by
// rule index (none for plugin rules), or a *CrashError.
func runRules(ctx *Context, rules []EnabledRule) (perRule [][]Finding, err error) {
	defer recoverCrash(&err)
	timing := ctx.Timing
	perRule = make([][]Finding, len(rules))
	var syntaxRules, fileRules []int
	var wg sync.WaitGroup
	var syntaxErr error
	for i, enabled := range rules {
		switch {
		case enabled.Rule.Visit != nil:
			syntaxRules = append(syntaxRules, i)
		case enabled.Rule.CheckFile != nil:
			fileRules = append(fileRules, i)
		}
	}
	// Syntax rules never touch a checker, so they run alongside the others.
	// Checker-backed rules run one after another, since each checker must
	// only be used by one goroutine at a time.
	if len(syntaxRules) > 0 {
		wg.Go(func() {
			defer recoverCrash(&syntaxErr)
			start := time.Now()
			pprof.SetGoroutineLabels(pprof.WithLabels(context.Background(), pprof.Labels("rule", "syntax rules")))
			runSyntaxRules(ctx, rules, syntaxRules, perRule)
			timing.Span("syntax rules", time.Since(start))
		})
	}
	// Wait for the syntax rules before raising a crash of another rule.
	defer wg.Wait()
	for i, enabled := range rules {
		if enabled.Rule.Check != nil {
			start := time.Now()
			// The label tells the rules apart in a CPU profile, whose
			// stacks are cut off below the rule's functions.
			pprof.Do(context.Background(), pprof.Labels("rule", enabled.Rule.Name), func(context.Context) {
				defer rethrow(enabled.Rule.Name, nil)
				perRule[i] = enabled.Rule.Check(ctx, enabled.Options)
			})
			timing.Span(enabled.Rule.Name, time.Since(start))
		}
	}
	if len(fileRules) > 0 {
		start := time.Now()
		pprof.Do(context.Background(), pprof.Labels("rule", "file rules"), func(context.Context) {
			runFileRules(ctx, rules, fileRules, perRule)
		})
		timing.Span("file rules", time.Since(start))
	}
	wg.Wait()
	if syntaxErr != nil {
		return nil, syntaxErr
	}
	timing.Mark("rules")
	return perRule, nil
}

// collectResults turns the findings (by rule index) that aren't suppressed
// by ignore comments into sorted results, and reports the ignore comments
// that suppress nothing when unused-ignore is enabled.
func collectResults(ctx *Context, config *Config, perRule [][]Finding) []Result {
	rules := config.Rules
	var results []Result
	used := map[ignoreUse]bool{}
	enabled := map[string]bool{}
	ignoreRule := -1
	for i, findings := range perRule {
		name := rules[i].Rule.Name
		enabled[name] = true
		if name == unusedIgnore {
			ignoreRule = i
		}
		for _, f := range findings {
			pos := f.Pos
			var comment int
			var ignored bool
			if f.Node != nil {
				pos = Start(f.Node, f.File.Source)
				comment, ignored = isIgnored(f.File, f.Node, name)
			} else {
				comment, ignored = isIgnoredAt(f.File, pos, name)
			}
			if ignored {
				used[ignoreUse{f.File, comment, ""}] = true
				used[ignoreUse{f.File, comment, name}] = true
				continue
			}
			line, column := f.File.LineAndColumn(pos)
			results = append(results, Result{name, rules[i].Severity, f.File.RelPath, line + 1, column + 1, f.Message})
		}
	}
	// Its own findings can't be ignored: that would take an ignore comment
	// for the ignore comment.
	if ignoreRule >= 0 {
		for _, f := range unusedIgnores(ctx.Files, config.knownRules, enabled, used) {
			line, column := f.File.LineAndColumn(f.Pos)
			results = append(results, Result{unusedIgnore, rules[ignoreRule].Severity, f.File.RelPath, line + 1, column + 1, f.Message})
		}
	}
	slices.SortStableFunc(results, func(a, b Result) int {
		return cmp.Or(strings.Compare(a.File, b.File), a.Line-b.Line, a.Column-b.Column, strings.Compare(a.Rule, b.Rule))
	})
	return results
}

// runSyntaxRules does one traversal per non-test file (files in parallel)
// for all syntax rules.
func runSyntaxRules(ctx *Context, rules []EnabledRule, indexes []int, perRule [][]Finding) {
	perFile := make([][][]Finding, len(ctx.Files))
	ctx.Parallel(len(ctx.Files), func(fi int) {
		file := ctx.Files[fi]
		if file.IsTest {
			return
		}
		findings, reports := fileReports(file, len(indexes))
		current := ""
		defer func() {
			if r := recover(); r != nil {
				panic(asCrash(r, current, file))
			}
		}()
		var visit func(node *ast.Node) bool
		visit = func(node *ast.Node) bool {
			for k, ri := range indexes {
				current = rules[ri].Rule.Name
				rules[ri].Rule.Visit(file, node, rules[ri].Options, reports[k])
			}
			node.ForEachChild(visit)
			return false
		}
		visit(file.Source.AsNode())
		perFile[fi] = findings
	})
	collectPerFile(perFile, indexes, perRule)
}

// runFileRules runs all per-file checker rules in one parallel pass over
// the non-test files.
func runFileRules(ctx *Context, rules []EnabledRule, indexes []int, perRule [][]Finding) {
	perFile := make([][][]Finding, len(ctx.Files))
	var files []*File
	for _, file := range ctx.Files {
		if !file.IsTest {
			files = append(files, file)
		}
	}
	ctx.ForEachFile(files, func(file *File, ch *checker.Checker) {
		findings, reports := fileReports(file, len(indexes))
		for k, ri := range indexes {
			func() {
				defer rethrow(rules[ri].Rule.Name, file)
				rules[ri].Rule.CheckFile(file, ch, rules[ri].Options, reports[k])
			}()
		}
		perFile[file.Index] = findings
	})
	collectPerFile(perFile, indexes, perRule)
}

// fileReports returns count reports for findings in file, and the findings
// they record, by report.
func fileReports(file *File, count int) ([][]Finding, []Report) {
	findings := make([][]Finding, count)
	reports := make([]Report, count)
	for k := range reports {
		reports[k] = func(node *ast.Node, message string) {
			findings[k] = append(findings[k], Finding{File: file, Node: node, Message: message})
		}
	}
	return findings, reports
}

// collectPerFile adds the findings of each file (by position among indexes)
// to perRule, in file order. Files the rules skipped have no findings.
func collectPerFile(perFile [][][]Finding, indexes []int, perRule [][]Finding) {
	for k, ri := range indexes {
		for _, findings := range perFile {
			if findings != nil {
				perRule[ri] = append(perRule[ri], findings[k]...)
			}
		}
	}
}

// Format renders results as aligned columns plus a summary line.
func Format(results []Result) string {
	if len(results) == 0 {
		return "No problems found"
	}
	rows := make([][4]string, len(results))
	var widths [3]int
	errors := 0
	for i, r := range results {
		severity := "warning"
		if r.Severity == "error" {
			severity = "error"
			errors++
		}
		rows[i] = [4]string{fmt.Sprintf("%s:%d:%d", r.File, r.Line, r.Column), severity, r.Rule, r.Message}
		for c := range widths {
			widths[c] = max(widths[c], len(rows[i][c]))
		}
	}
	var b strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&b, "%-*s  %-*s  %-*s  %s\n", widths[0], row[0], widths[1], row[1], widths[2], row[2], row[3])
	}
	fmt.Fprintf(&b, "\n%s, %s", plural(errors, "error"), plural(len(rows)-errors, "warning"))
	return b.String()
}

func plural(count int, word string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, word)
	}
	return fmt.Sprintf("%d %ss", count, word)
}
