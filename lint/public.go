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
	c.markDeclarations(entries, func(file *File) *map[int]bool { return &file.public }, func(file *File, _ *checker.Checker) []*ast.Symbol {
		return []*ast.Symbol{file.Source.Symbol}
	})
}

// markDeclarations marks the declarations that the symbols of each file
// stand for (see forEachDeclaration) in the marks of their files, which
// field returns.
func (c *Context) markDeclarations(files []*File, field func(file *File) *map[int]bool, symbols func(file *File, ch *checker.Checker) []*ast.Symbol) {
	var mu sync.Mutex
	c.ForEachFile(files, func(file *File, ch *checker.Checker) {
		visited := map[*ast.Symbol]bool{}
		for _, symbol := range symbols(file, ch) {
			forEachDeclaration(ch, symbol, visited, func(declaration *ast.Node) {
				target := c.filesByPath[ast.GetSourceFileOfNode(declaration).Path()]
				if target == nil {
					return
				}
				mu.Lock()
				marks := field(target)
				if *marks == nil {
					*marks = map[int]bool{}
				}
				(*marks)[declaration.Pos()] = true
				mu.Unlock()
			})
		}
	})
}

// forEachDeclaration calls fn with the declarations of symbol and of what it
// stands for: the symbols it aliases, in turn, the exports of a module, and
// the classes that a class extends.
func forEachDeclaration(ch *checker.Checker, symbol *ast.Symbol, visited map[*ast.Symbol]bool, fn func(declaration *ast.Node)) {
	if symbol == nil || visited[symbol] {
		return
	}
	visited[symbol] = true
	for _, declaration := range symbol.Declarations {
		if declaration.Kind != ast.KindSourceFile {
			fn(declaration)
			continue
		}
		for _, export := range ch.GetExportsOfModule(symbol) {
			forEachDeclaration(ch, export, visited, fn)
		}
	}
	if symbol.Flags&ast.SymbolFlagsAlias != 0 {
		// The default of a module imported with esModuleInterop is an alias
		// the checker makes up, without declarations or a target to resolve.
		if len(symbol.Declarations) > 0 {
			forEachDeclaration(ch, ch.GetImmediateAliasedSymbol(symbol), visited, fn)
		}
		return
	}
	if symbol.Flags&ast.SymbolFlagsClass != 0 {
		for _, base := range ch.GetBaseTypes(ch.GetDeclaredTypeOfSymbol(symbol)) {
			forEachDeclaration(ch, base.Symbol(), visited, fn)
		}
	}
}
