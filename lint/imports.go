package lint

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
)

// maxListedImports is how many unresolved modules the error lists.
const maxListedImports = 10

// checkImports returns an error when imports in the analyzed files don't
// resolve, which usually means dependencies aren't installed. Without the
// imported types, usages can't be linked to declarations, and rules would
// report used code as unused. An import counts as resolved when it resolves
// to a file or matches a `declare module`, like it does for tsc. Side-effect
// imports (import "./styles.css") bring in no types, so they're skipped.
func checkImports(ctx *Context) error {
	type unresolved struct {
		file   *File
		module *ast.Node
	}
	var found []unresolved
	seen := map[string]bool{}
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
			if seen[name] || isSideEffectImport(module) || program.GetResolvedModuleFromModuleSpecifier(file.Source, module).IsResolved() {
				continue
			}
			if matchesPattern(patterns, name) {
				continue
			}
			seen[name] = true
			found = append(found, unresolved{file, module})
		}
	}
	if len(found) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Cannot find %s; install dependencies or fix the imports:\n", plural(len(found), "imported module"))
	for i, u := range found {
		if i == maxListedImports {
			fmt.Fprintf(&b, "  and %d more\n", len(found)-i)
			break
		}
		line, column := u.file.LineAndColumn(Start(u.module, u.file.Source))
		fmt.Fprintf(&b, "  %s  %s:%d:%d\n", u.module.Text(), u.file.RelPath, line+1, column+1)
	}
	return errors.New(strings.TrimSuffix(b.String(), "\n"))
}

func isSideEffectImport(module *ast.Node) bool {
	return module.Parent.Kind == ast.KindImportDeclaration && module.Parent.AsImportDeclaration().ImportClause == nil
}

func matchesPattern(patterns []core.Pattern, name string) bool {
	for i := range patterns {
		if patterns[i].Matches(name) {
			return true
		}
	}
	return false
}
