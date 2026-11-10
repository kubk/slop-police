package lint

import (
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/tspath"
)

// ImportedFile returns the source file that module, the specifier of one of
// the file's imports, resolves to in the file's project, or nil.
func (f *File) ImportedFile(module *ast.Node) *ast.SourceFile {
	program := f.Project.Program
	resolved := program.GetResolvedModuleFromModuleSpecifier(f.Source, module)
	if !resolved.IsResolved() {
		return nil
	}
	// Through a project reference, the import resolves to the referenced
	// project's output, and the program has its source instead.
	return program.GetSourceFileForResolvedModule(resolved.ResolvedFileName)
}

// IsNamespaceOfMember reports whether node is the namespace in a member
// access: ns in ns.member, in an expression or a type.
func IsNamespaceOfMember(node *ast.Node) bool {
	parent := node.Parent
	switch parent.Kind {
	case ast.KindPropertyAccessExpression:
		return parent.Expression() == node
	case ast.KindQualifiedName:
		return parent.AsQualifiedName().Left == node
	}
	return false
}

// HasModuleObjectValue reports whether the file's exports are properties of
// a module object that may be used as a value.
//
// A module object is what import * as ns, import() and require() give: an
// object with the module's exports as properties. An export read as ns.name
// is resolved from the imports alone. Once the object is a value (the result
// of import(), a namespace passed on or stored), an export is read from it
// as a property of some expression, and resolving that takes the type of the
// expression, which is slow. Rules resolve the properties of values only for
// the names that such an object can have (see isValueMember in the rules).
//
// The objects are found from the syntax, so there may be more of them than
// are really used as values: those of the files loaded with import() or
// require(), named by an import() type, re-exported as a namespace, or
// imported as a namespace whose name is used other than as ns.member. Such
// an object also has the exports of the files that its file re-exports with
// export *.
func (c *Context) HasModuleObjectValue(file *File) bool {
	c.moduleObjectsOnce.Do(c.findModuleObjectValues)
	return c.moduleObjectExports[file.Source.Path()]
}

// IsReexportedByModuleObjectValue reports whether a module object that may
// be used as a value (see HasModuleObjectValue) may have what the file
// declares under some name: the file's exports are its properties, or it
// gets to them through re-exports of any kind.
func (c *Context) IsReexportedByModuleObjectValue(file *File) bool {
	c.moduleObjectsOnce.Do(c.findModuleObjectValues)
	return c.moduleObjectReexports[file.Source.Path()]
}

func (c *Context) findModuleObjectValues() {
	files := append(slices.Clip(c.Files), c.Unanalyzed...)
	indexes := make(map[tspath.Path]int, len(files))
	for i, file := range files {
		indexes[file.Source.Path()] = i
	}
	perFile := make([]moduleObjectUses, len(files))
	c.Parallel(len(files), func(i int) {
		perFile[i] = findModuleObjectUses(files[i], func(path tspath.Path) bool {
			_, ok := indexes[path]
			return ok
		})
	})
	// reachable returns the files with a module object that may be a value,
	// and the files that those get to over the edges.
	reachable := func(edges func(uses moduleObjectUses) []tspath.Path) map[tspath.Path]bool {
		found := map[tspath.Path]bool{}
		var queue []tspath.Path
		add := func(paths []tspath.Path) {
			for _, path := range paths {
				if !found[path] {
					found[path] = true
					queue = append(queue, path)
				}
			}
		}
		for _, uses := range perFile {
			add(uses.values)
		}
		for len(queue) > 0 {
			path := queue[0]
			queue = queue[1:]
			add(edges(perFile[indexes[path]]))
		}
		return found
	}
	c.moduleObjectExports = reachable(func(uses moduleObjectUses) []tspath.Path { return uses.starred })
	c.moduleObjectReexports = reachable(func(uses moduleObjectUses) []tspath.Path { return uses.reexported })
}

// moduleObjectUses are the files whose module object a file may use as a
// value, the files it re-exports with export *, and the files it re-exports
// from in any way: with export ... from, or by exporting what it imported.
type moduleObjectUses struct{ values, starred, reexported []tspath.Path }

// findModuleObjectUses finds the file's moduleObjectUses among the files
// that isFile accepts.
func findModuleObjectUses(file *File, isFile func(path tspath.Path) bool) moduleObjectUses {
	var values, starred, reexported []tspath.Path
	namespaces := map[string]tspath.Path{} // namespace imports by local name
	imports := map[string]tspath.Path{}    // all import bindings by local name
	for _, module := range file.Source.Imports() {
		target := file.ImportedFile(module)
		// Imports that the compiler adds, like the JSX runtime's, have no
		// parent.
		if target == nil || !isFile(target.Path()) || module.Parent == nil {
			continue
		}
		path := target.Path()
		switch parent := module.Parent; parent.Kind {
		case ast.KindImportDeclaration, ast.KindJSImportDeclaration:
			clause := parent.ImportClause()
			if clause == nil {
				continue
			}
			if name := clause.Name(); name != nil {
				imports[name.Text()] = path
			}
			switch bindings := clause.AsImportClause().NamedBindings; {
			case bindings == nil:
			case bindings.Kind == ast.KindNamespaceImport:
				namespaces[bindings.Name().Text()] = path
				imports[bindings.Name().Text()] = path
			default:
				for _, specifier := range bindings.AsNamedImports().Elements.Nodes {
					imports[specifier.Name().Text()] = path
				}
			}
		case ast.KindExportDeclaration:
			reexported = append(reexported, path)
			switch clause := parent.AsExportDeclaration().ExportClause; {
			case clause == nil:
				starred = append(starred, path)
			case clause.Kind == ast.KindNamespaceExport:
				values = append(values, path)
			}
		case ast.KindLiteralType:
			// import("./x").Type names a member, like ns.Type.
			if parent.Parent.Kind != ast.KindImportType || parent.Parent.AsImportTypeNode().Qualifier == nil {
				values = append(values, path)
			}
		default:
			// import(), require() and import x = require().
			values = append(values, path)
		}
	}

	for _, statement := range file.Source.Statements.Nodes {
		switch statement.Kind {
		case ast.KindExportDeclaration:
			declaration := statement.AsExportDeclaration()
			if declaration.ModuleSpecifier != nil || declaration.ExportClause == nil || declaration.ExportClause.Kind != ast.KindNamedExports {
				continue
			}
			for _, specifier := range declaration.ExportClause.AsNamedExports().Elements.Nodes {
				if path, ok := imports[specifier.PropertyNameOrName().Text()]; ok {
					reexported = append(reexported, path)
				}
			}
		case ast.KindExportAssignment:
			if expression := statement.Expression(); expression.Kind == ast.KindIdentifier {
				if path, ok := imports[expression.Text()]; ok {
					reexported = append(reexported, path)
				}
			}
		}
	}

	if len(namespaces) > 0 {
		var visit func(node *ast.Node) bool
		visit = func(node *ast.Node) bool {
			if node.Kind == ast.KindIdentifier && node.Parent.Kind != ast.KindNamespaceImport && !IsNamespaceOfMember(node) {
				if path, ok := namespaces[node.Text()]; ok {
					values = append(values, path)
					delete(namespaces, node.Text())
				}
			}
			return len(namespaces) == 0 || node.ForEachChild(visit)
		}
		visit(file.Source.AsNode())
	}
	return moduleObjectUses{values, starred, reexported}
}
