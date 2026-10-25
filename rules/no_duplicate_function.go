package rules

import (
	"cmp"
	"fmt"
	"hash/maphash"
	"io"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// no-duplicate-function reports functions whose body has the same shape as
// functions in at least two other files: a helper that was rewritten instead of reused. Shapes ignore local
// names and literal values, but not the names of what the body uses.
const (
	// Bodies below these sizes repeat by coincidence.
	minDuplicateStatements = 2
	minDuplicateNodes      = 16
	maxListedPeers         = 3
)

func init() {
	lint.Register(&lint.Rule{
		Name: "no-duplicate-function",
		// Two copies may be a deliberate fork; three are a pattern.
		DefaultOptions: lint.Options{"minFiles": 3.0},
		Check:          checkDuplicateFunctions,
	})
}

type bodyShape struct {
	async      bool
	parameters int
	statements int
	hash       uint64
}

type shapedFunction struct {
	file  *lint.File
	fn    checkedFunction
	shape bodyShape
	line  int // 1-based, of the name
}

func checkDuplicateFunctions(ctx *lint.Context, options lint.Options) []lint.Finding {
	// A shape in a single file is a duplicate only within that file, which
	// this rule doesn't look for.
	minFiles := max(2, int(options.Number("minFiles")))
	seed := maphash.MakeSeed()
	perFile := make([][]shapedFunction, len(ctx.Files))
	ctx.Parallel(len(ctx.Files), func(i int) {
		if !ctx.Files[i].IsTest {
			perFile[i] = shapeFunctions(ctx.Files[i], seed)
		}
	})

	clusters := map[bodyShape][]*shapedFunction{}
	for _, functions := range perFile {
		for i := range functions {
			f := &functions[i]
			clusters[f.shape] = append(clusters[f.shape], f)
		}
	}

	var findings []lint.Finding
	for _, cluster := range clusters {
		if len(cluster) < minFiles || countFiles(cluster) < minFiles {
			continue
		}
		slices.SortFunc(cluster, func(a, b *shapedFunction) int {
			return cmp.Or(strings.Compare(a.file.RelPath, b.file.RelPath), a.fn.Name.Pos()-b.fn.Name.Pos())
		})
		for _, f := range cluster {
			message := fmt.Sprintf("%s has the same body shape as %s", f.fn.Label(f.file), listPeers(f, cluster))
			findings = append(findings, lint.Finding{File: f.file, Node: f.fn.Name, Message: message})
		}
	}
	return findings
}

// shapeFunctions fingerprints the file's checked functions that are large
// enough to count as duplicates.
func shapeFunctions(file *lint.File, seed maphash.Seed) []shapedFunction {
	var result []shapedFunction
	locals := map[string]bool{}
	var hash maphash.Hash
	hash.SetSeed(seed)
	forEachNode(file.Source.AsNode(), func(node *ast.Node) {
		fn, ok := functionAt(node)
		if !ok {
			return
		}
		// Two or more statements also rules out pass-through wrappers, which
		// are a single return.
		statements := len(fn.Body.Statements())
		if statements < minDuplicateStatements || !hasAtLeastNodes(fn.Body, minDuplicateNodes) {
			return
		}
		clear(locals)
		collectLocalNames(fn, locals)
		hash.Reset()
		writeShape(&hash, fn.Body, locals)
		result = append(result, shapedFunction{
			file:  file,
			fn:    fn,
			shape: bodyShape{hasModifier(fn.Function, ast.KindAsyncKeyword), len(fn.Function.Parameters()), statements, hash.Sum64()},
			line:  file.Line(lint.Start(fn.Name, file.Source)) + 1,
		})
	})
	return result
}

// hasAtLeastNodes reports whether node's subtree (node included) has at
// least n nodes, without walking more of it than needed.
func hasAtLeastNodes(node *ast.Node, n int) bool {
	count := 0
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		count++
		return count >= n || node.ForEachChild(visit)
	}
	return visit(node)
}

// collectLocalNames adds the names a function binds itself: identifier
// parameters, and anywhere in the body (nested functions included)
// identifier variables, catch variables and function and class declarations.
func collectLocalNames(fn checkedFunction, locals map[string]bool) {
	for _, parameter := range fn.Function.Parameters() {
		if name := parameter.Name(); name.Kind == ast.KindIdentifier {
			locals[name.Text()] = true
		}
	}
	forEachNode(fn.Body, func(node *ast.Node) {
		switch node.Kind {
		case ast.KindVariableDeclaration, ast.KindFunctionDeclaration, ast.KindClassDeclaration:
			// Catch variables are variable declarations too.
			if name := node.Name(); name != nil && name.Kind == ast.KindIdentifier {
				locals[name.Text()] = true
			}
		}
	})
}

// writeShape serializes node in forEachChild order as kinds and parentheses,
// with identifiers as "local" or their name. Other leaves, literals
// included, are written as their kind only, so literal values don't count.
func writeShape(w io.StringWriter, node *ast.Node, locals map[string]bool) {
	switch node.Kind {
	case ast.KindIdentifier:
		if locals[node.Text()] {
			w.WriteString("local")
		} else {
			w.WriteString("id:")
			w.WriteString(node.Text())
		}
		return
	case ast.KindPrivateIdentifier:
		w.WriteString("private")
		return
	}
	w.WriteString(node.Kind.String())
	first := true
	node.ForEachChild(func(child *ast.Node) bool {
		if first {
			w.WriteString("(")
			first = false
		} else {
			w.WriteString(",")
		}
		writeShape(w, child, locals)
		return false
	})
	if !first {
		w.WriteString(")")
	}
}

func countFiles(cluster []*shapedFunction) int {
	files := map[*lint.File]bool{}
	for _, f := range cluster {
		files[f.file] = true
	}
	return len(files)
}

// listPeers names up to maxListedPeers other functions of the (sorted)
// cluster, those in other files first.
func listPeers(self *shapedFunction, cluster []*shapedFunction) string {
	var listed []string
	for _, sameFile := range []bool{false, true} {
		for _, f := range cluster {
			if len(listed) == maxListedPeers {
				break
			}
			if f != self && (f.file == self.file) == sameFile {
				listed = append(listed, fmt.Sprintf("%s (%s:%d)", peerName(f), f.file.RelPath, f.line))
			}
		}
	}
	text := strings.Join(listed, ", ")
	if more := len(cluster) - 1 - len(listed); more > 0 {
		text += fmt.Sprintf(" and %d more", more)
	}
	return text
}

func peerName(f *shapedFunction) string {
	if f.fn.Name == f.fn.Function {
		return "default export"
	}
	return lint.Text(f.fn.Name, f.file.Source)
}
