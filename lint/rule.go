package lint

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

type OptionDef struct {
	Name    string
	Default any // bool, float64 or string
}

type Options map[string]any

func (o Options) Bool(name string) bool {
	value, _ := o[name].(bool)
	return value
}

func (o Options) Number(name string) float64 {
	value, _ := o[name].(float64)
	return value
}

// Report records a finding at node (reported at the node's start position).
type Report func(node *ast.Node, message string)

// A Rule implements exactly one of Visit, CheckFile or Check:
//
//   - Visit: syntax only. Called for every node of every analyzed file, in a
//     single traversal shared by all syntax rules, files in parallel.
//   - CheckFile: per file, with the type checker that owns the file. Files
//     run in parallel.
//   - Check: whole program. Gets the Context and uses its helpers
//     (ForEachFile, Parallel, AnyReference) for cross-file analyses.
type Rule struct {
	Name           string
	DefaultOptions []OptionDef

	Visit     func(file *File, node *ast.Node, options Options, report Report)
	CheckFile func(file *File, ch *checker.Checker, options Options, report Report)
	Check     func(ctx *Context, options Options) []Finding
}

func (r *Rule) defaultOption(name string) (any, bool) {
	for _, option := range r.DefaultOptions {
		if option.Name == name {
			return option.Default, true
		}
	}
	return nil, false
}

// Finding is a rule result before ignore comments and sorting are applied.
// It is reported at Node's start, or at the byte offset Pos when Node is nil
// (for what isn't a node, such as a comment).
type Finding struct {
	File    *File
	Node    *ast.Node
	Pos     int
	Message string
}

var registry = map[string]*Rule{}

func Register(rule *Rule) {
	kinds := 0
	for _, set := range []bool{rule.Visit != nil, rule.CheckFile != nil, rule.Check != nil} {
		if set {
			kinds++
		}
	}
	if rule.Name == "" || kinds != 1 || registry[rule.Name] != nil {
		panic("invalid or duplicate rule " + rule.Name)
	}
	registry[rule.Name] = rule
}
