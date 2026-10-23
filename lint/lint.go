package lint

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

type Result struct {
	Rule     string
	Severity Severity
	File     string
	Line     int // 1-based
	Column   int // 1-based, in UTF-16 code units
	Message  string
}

// Run lints the project in root (reading root/slop-police.json), prints the
// report to stdout and returns the exit code: 0, 1 when there are errors, 2
// for config errors (printed to stderr).
func Run(root string, stdout, stderr io.Writer, timing *Timing) int {
	config, err := LoadConfig(root)
	var results []Result
	if err == nil {
		results, err = Lint(root, config, timing)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.As(err, new(*ConfigError)) {
			return 2
		}
		return 1
	}
	fmt.Fprintln(stdout, Format(results))
	for _, r := range results {
		if r.Severity == "error" {
			return 1
		}
	}
	return 0
}

// Lint runs every enabled rule and returns the sorted findings that are not
// suppressed by ignore comments.
func Lint(root string, config *Config, timing *Timing) ([]Result, error) {
	if len(config.Rules) == 0 {
		return nil, nil
	}
	ctx, err := NewContext(root, config, timing)
	if err != nil {
		return nil, err
	}
	timing.Mark(fmt.Sprintf("collect files (%d)", len(ctx.Files)))

	perRule := make([][]Finding, len(config.Rules))
	var syntaxRules, fileRules []int
	var wg sync.WaitGroup
	for i, enabled := range config.Rules {
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
			start := time.Now()
			runSyntaxRules(ctx, config.Rules, syntaxRules, perRule)
			timing.Span("syntax rules", time.Since(start))
		})
	}
	for i, enabled := range config.Rules {
		if enabled.Rule.Check != nil {
			start := time.Now()
			perRule[i] = enabled.Rule.Check(ctx, enabled.Options)
			timing.Span(enabled.Rule.Name, time.Since(start))
		}
	}
	if len(fileRules) > 0 {
		start := time.Now()
		runFileRules(ctx, config.Rules, fileRules, perRule)
		timing.Span("file rules", time.Since(start))
	}
	wg.Wait()
	timing.Mark("rules")

	var results []Result
	for i, findings := range perRule {
		name := config.Rules[i].Rule.Name
		for _, f := range findings {
			pos := f.Pos
			if f.Node != nil {
				pos = Start(f.Node, f.File.Source)
			}
			if f.Node != nil && IsIgnored(f.File, f.Node, name) || f.Node == nil && IsIgnoredAt(f.File, pos, name) {
				continue
			}
			line, column := f.File.LineAndColumn(pos)
			results = append(results, Result{name, config.Rules[i].Severity, f.File.RelPath, line + 1, column + 1, f.Message})
		}
	}
	slices.SortStableFunc(results, func(a, b Result) int {
		return cmp.Or(strings.Compare(a.File, b.File), a.Line-b.Line, a.Column-b.Column, strings.Compare(a.Rule, b.Rule))
	})
	return results, nil
}

// runSyntaxRules does one traversal per file (files in parallel) for all
// syntax rules.
func runSyntaxRules(ctx *Context, rules []EnabledRule, indexes []int, perRule [][]Finding) {
	perFile := make([][][]Finding, len(ctx.Files))
	ctx.Parallel(len(ctx.Files), func(fi int) {
		file := ctx.Files[fi]
		findings := make([][]Finding, len(indexes))
		reports := make([]Report, len(indexes))
		for k := range indexes {
			reports[k] = func(node *ast.Node, message string) {
				findings[k] = append(findings[k], Finding{File: file, Node: node, Message: message})
			}
		}
		var visit func(node *ast.Node) bool
		visit = func(node *ast.Node) bool {
			for k, ri := range indexes {
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

// runFileRules runs all per-file checker rules in one parallel pass.
func runFileRules(ctx *Context, rules []EnabledRule, indexes []int, perRule [][]Finding) {
	perFile := make([][][]Finding, len(ctx.Files))
	ctx.ForEachFile(ctx.Files, func(file *File, ch *checker.Checker) {
		findings := make([][]Finding, len(indexes))
		for k, ri := range indexes {
			rules[ri].Rule.CheckFile(file, ch, rules[ri].Options, func(node *ast.Node, message string) {
				findings[k] = append(findings[k], Finding{File: file, Node: node, Message: message})
			})
		}
		perFile[file.Index] = findings
	})
	collectPerFile(perFile, indexes, perRule)
}

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
