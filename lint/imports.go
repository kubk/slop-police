package lint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/tspath"
)

// maxListedImports is how many unresolved modules the error lists.
const maxListedImports = 10

// unresolvedImport reports relative imports of files that don't exist (see
// checkImports), which collectResults takes from the Context.
const unresolvedImport = "no-unresolved-import"

func init() {
	Register(&Rule{Name: unresolvedImport, Check: func(ctx *Context, _ Options) []Finding { return ctx.missingImports }})
}

// checkImports returns an error when imports in the analyzed files don't
// resolve, which usually means dependencies aren't installed. Without the
// imported types, usages can't be linked to declarations, and rules would
// report used code as unused. An import counts as resolved when it resolves
// to a file or matches a `declare module`, like it does for tsc. Side-effect
// imports (import "./styles.css") bring in no types, so they're skipped, and
// so are imports under a @ts-expect-error or @ts-ignore, whose author knows
// they don't resolve (virtual modules, runtime-provided ones).
//
// A relative import of a path where no file exists, under any extension,
// hides no usages: there's nothing it could link to. It's a bug in the
// code, not in the setup, so it becomes a no-unresolved-import finding
// instead of failing the run. When a file is there but doesn't resolve
// (like ./util.js for util.ts under the wrong moduleResolution), the setup
// is wrong and the run fails.
func checkImports(ctx *Context) error {
	type unresolved struct {
		file   *File
		module *ast.Node
	}
	type key struct {
		project *Project
		name    string
	}
	var found []unresolved
	seen := map[key]bool{}
	ambient := map[*Project][]core.Pattern{}
	files := slices.SortedFunc(slices.Values(ctx.Files), func(a, b *File) int { return strings.Compare(a.RelPath, b.RelPath) })
	for _, file := range files {
		program := file.Project.Program
		patterns, ok := ambient[file.Project]
		if !ok {
			for _, sf := range program.GetSourceFiles() {
				for _, name := range sf.AmbientModuleNames {
					if pattern := core.TryParsePattern(name); pattern.IsValid() {
						patterns = append(patterns, pattern)
					}
				}
			}
			ambient[file.Project] = patterns
		}
		for _, module := range file.Source.Imports() {
			name := module.Text()
			if isSideEffectImport(module) || program.GetResolvedModuleFromModuleSpecifier(file.Source, module).IsResolved() {
				continue
			}
			if matchesPattern(patterns, name) || hasPrecedingDirective(file, module) {
				continue
			}
			if tspath.IsExternalModuleNameRelative(name) && !anyFileAt(tspath.ResolvePath(tspath.GetDirectoryPath(file.Source.FileName()), name)) {
				ctx.missingImports = append(ctx.missingImports, Finding{File: file, Node: module, Message: "Cannot find module " + name})
				continue
			}
			// A package missing in one project is likely missing in the
			// others too, but each project resolves it on its own terms.
			if k := (key{file.Project, name}); !seen[k] {
				seen[k] = true
				found = append(found, unresolved{file, module})
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Cannot find %s; install dependencies or fix the imports:\n", plural(len(found), "imported module"))
	builtins := false
	for i, u := range found {
		builtins = builtins || core.NodeCoreModules()[u.module.Text()]
		if i == maxListedImports {
			fmt.Fprintf(&b, "  and %d more\n", len(found)-i)
			continue
		}
		if i < maxListedImports {
			line, column := u.file.LineAndColumn(Start(u.module, u.file.Source))
			fmt.Fprintf(&b, "  %s  %s:%d:%d\n", u.module.Text(), u.file.RelPath, line+1, column+1)
		}
	}
	if builtins {
		b.WriteString(`Node.js built-ins resolve only when the file's tsconfig lists "node" in compilerOptions.types: TypeScript 7 no longer loads every @types package by default` + "\n")
	}
	return errors.New(strings.TrimSuffix(b.String(), "\n"))
}

// anyFileAt reports whether a file or directory exists at path, with any
// extension added or swapped for another, like ./util.js for util.ts.
func anyFileAt(path string) bool {
	dir, base := filepath.Split(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	for _, entry := range entries {
		name := entry.Name()
		if name == base || strings.HasPrefix(name, base+".") || stem != "" && strings.HasPrefix(name, stem+".") {
			return true
		}
	}
	return false
}

func isSideEffectImport(module *ast.Node) bool {
	return module.Parent.Kind == ast.KindImportDeclaration && module.Parent.AsImportDeclaration().ImportClause == nil
}

// hasPrecedingDirective reports whether tsc would suppress an error at the
// module specifier: a @ts-expect-error or @ts-ignore comment is on a line
// above it, with only comments or blank lines in between.
func hasPrecedingDirective(file *File, module *ast.Node) bool {
	if len(file.Source.CommentDirectives) == 0 {
		return false
	}
	directiveLines := map[int]bool{}
	for _, directive := range file.Source.CommentDirectives {
		directiveLines[file.Line(directive.Loc.Pos())] = true
	}
	text := file.Source.Text()
	for line := file.Line(Start(module, file.Source)) - 1; line >= 0; line-- {
		if directiveLines[line] {
			return true
		}
		if !isCommentOrBlankLine(text, int(file.lineStarts[line])) {
			return false
		}
	}
	return false
}

func isCommentOrBlankLine(text string, pos int) bool {
	for pos < len(text) && (text[pos] == ' ' || text[pos] == '\t') {
		pos++
	}
	return pos == len(text) || text[pos] == '\r' || text[pos] == '\n' ||
		pos+1 < len(text) && text[pos] == '/' && text[pos+1] == '/'
}

func matchesPattern(patterns []core.Pattern, name string) bool {
	for i := range patterns {
		if patterns[i].Matches(name) {
			return true
		}
	}
	return false
}
