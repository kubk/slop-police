package lint

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// IsPublic reports whether declaration (a node of the file) is what an entry
// point exports, or a class that such a class extends: something outside
// the analyzed code may use it and its members. Declarations aren't public
// without the entry option.
func (f *File) IsPublic(declaration *ast.Node) bool {
	return f.public[declaration.Pos()]
}

// markPublicDeclarations marks the declarations that the entry points
// export, also through re-exports, export * and namespace exports, and the
// classes the exported classes extend.
func (c *Context) markPublicDeclarations() {
	var entries []*File
	for _, file := range c.Files {
		if file.IsEntry && !file.IsTest {
			entries = append(entries, file)
		}
	}
	var mu sync.Mutex
	c.ForEachFile(entries, func(file *File, ch *checker.Checker) {
		visited := map[*ast.Symbol]bool{}
		var visitModule func(module *ast.Symbol)
		var visit func(symbol *ast.Symbol)
		visit = func(symbol *ast.Symbol) {
			if symbol == nil || visited[symbol] {
				return
			}
			visited[symbol] = true
			if symbol.Flags&ast.SymbolFlagsAlias != 0 && len(symbol.Declarations) > 0 {
				visit(ch.GetAliasedSymbol(symbol))
				return
			}
			for _, declaration := range symbol.Declarations {
				if declaration.Kind == ast.KindSourceFile {
					visitModule(symbol)
					continue
				}
				target := c.filesByPath[ast.GetSourceFileOfNode(declaration).Path()]
				if target == nil {
					continue
				}
				mu.Lock()
				if target.public == nil {
					target.public = map[int]bool{}
				}
				target.public[declaration.Pos()] = true
				mu.Unlock()
			}
			if symbol.Flags&ast.SymbolFlagsClass != 0 {
				for _, base := range ch.GetBaseTypes(ch.GetDeclaredTypeOfSymbol(symbol)) {
					visit(base.Symbol())
				}
			}
		}
		visitModule = func(module *ast.Symbol) {
			for _, export := range ch.GetExportsOfModule(module) {
				visit(export)
			}
		}
		if module := file.Source.Symbol; module != nil {
			visitModule(module)
		}
	})
}
