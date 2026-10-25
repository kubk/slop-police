package rules

import (
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// unused-exports reports exports that no other analyzed file uses: the ones
// used only in their own file (the export keyword can go) and the ones never
// used at all. An import or re-export counts as a use, also in ignored files
// and declaration files.
//
// Entry points (a main.tsx, a framework page, a config file) have exports
// that something outside the analyzed code uses, so their exports aren't
// checked. Without the entry option, a file that no other analyzed file
// imports is an entry point. With it, only the listed files and tests are,
// and files they don't reach through imports are reported as never imported.
// Uses in those files don't count.
func init() {
	lint.Register(&lint.Rule{
		Name:           "unused-exports",
		DefaultOptions: lint.Options{"entry": []any{}},
		Check:          checkUnusedExports,
	})
}

type exportCandidate struct {
	file         *lint.File
	name         string
	node         *ast.Node
	declarations []*ast.Node
	usedInFile   atomic.Bool
	usedOutside  atomic.Bool
}

// moduleEdges are a file's imports of analyzed files, by how they use the
// imported module.
type moduleEdges struct {
	imports []tspath.Path // every import, including dynamic and type imports
	whole   []tspath.Path // dynamic imports, require and import types without a member: any export may be used
	star    []tspath.Path // export * from
}

func checkUnusedExports(ctx *lint.Context, options lint.Options) []lint.Finding {
	phase := ctx.Timing.Phase("unused-exports")
	files := append(slices.Clip(ctx.Files), ctx.Unanalyzed...)
	byPath := make(map[tspath.Path]*lint.File, len(files))
	for _, file := range files {
		byPath[file.Source.Path()] = file
	}
	edges := make([]moduleEdges, len(files))
	ctx.Parallel(len(files), func(i int) { edges[i] = collectModuleEdges(files[i], byPath) })

	// checked: analyzed files whose exports are checked. live: files whose
	// uses count.
	analyzed := len(ctx.Files)
	checked := make([]bool, len(files))
	live := make([]bool, len(files))
	var findings []lint.Finding
	if entries := options.Strings("entry"); len(entries) > 0 {
		// Declaration files take effect without being imported.
		var queue []*lint.File
		for _, file := range files {
			if file.IsTest || file.Source.IsDeclarationFile || lint.MatchesAny(entries, file.RelPath) {
				live[file.Index] = true
				queue = append(queue, file)
			}
		}
		for len(queue) > 0 {
			file := queue[0]
			queue = queue[1:]
			for _, path := range edges[file.Index].imports {
				if target := byPath[path]; !live[target.Index] {
					live[target.Index] = true
					checked[target.Index] = target.Index < analyzed && !target.IsTest
					queue = append(queue, target)
				}
			}
		}
		for _, file := range ctx.Files {
			if !live[file.Index] && ast.IsExternalModule(file.Source) && !hasModuleAugmentation(file.Source) {
				findings = append(findings, lint.Finding{File: file, Pos: 0, Message: "File is never imported from an entry point"})
			}
		}
	} else {
		for i := range files {
			live[i] = true
			for _, path := range edges[i].imports {
				if target := byPath[path]; target.Index < analyzed && !target.IsTest {
					checked[target.Index] = true
				}
			}
		}
	}

	var candidates []*exportCandidate
	byDecl := map[declKey]*exportCandidate{}
	wholeModule := map[tspath.Path]*atomic.Bool{}
	names := map[string]bool{}
	for _, file := range ctx.Files {
		if !checked[file.Index] {
			continue
		}
		wholeModule[file.Source.Path()] = &atomic.Bool{}
		for _, c := range collectExportCandidates(file, byDecl) {
			candidates = append(candidates, c)
			names[c.name] = true
		}
	}
	// An entry point's export * exposes everything the module exports.
	for _, file := range files {
		if live[file.Index] {
			star := edges[file.Index].star
			if checked[file.Index] {
				star = nil
			}
			for _, path := range append(edges[file.Index].whole, star...) {
				if used := wholeModule[path]; used != nil {
					used.Store(true)
				}
			}
		}
	}
	phase(fmt.Sprintf("candidates (%d)", len(candidates)))
	if len(candidates) == 0 {
		return findings
	}

	var liveFiles []*lint.File
	for _, file := range files {
		if live[file.Index] {
			liveFiles = append(liveFiles, file)
		}
	}
	ctx.ForEachFile(liveFiles, func(file *lint.File, ch *checker.Checker) {
		u := &exportUses{ch: ch, file: file, byDecl: byDecl, wholeModule: wholeModule, exportsEscape: !checked[file.Index]}
		u.collect(names)
	})
	phase("uses")

	for _, c := range candidates {
		if c.usedOutside.Load() || wholeModule[c.file.Source.Path()].Load() {
			continue
		}
		message := "Export " + c.name + " is never used"
		if c.name == "default" {
			message = "Default export is never used"
		}
		if c.usedInFile.Load() {
			message = "Export " + c.name + " is only used in its own file"
		}
		findings = append(findings, lint.Finding{File: c.file, Node: c.node, Message: message})
	}
	return findings
}

func collectModuleEdges(file *lint.File, byPath map[tspath.Path]*lint.File) moduleEdges {
	var edges moduleEdges
	program := file.Project.Program
	for _, module := range file.Source.Imports() {
		resolved := program.GetResolvedModuleFromModuleSpecifier(file.Source, module)
		if !resolved.IsResolved() {
			continue
		}
		target := program.GetSourceFile(resolved.ResolvedFileName)
		if target == nil || byPath[target.Path()] == nil || target.Path() == file.Source.Path() {
			continue
		}
		path := target.Path()
		edges.imports = append(edges.imports, path)
		parent := module.Parent
		switch {
		case parent.Kind == ast.KindCallExpression, parent.Kind == ast.KindExternalModuleReference:
			edges.whole = append(edges.whole, path)
		case parent.Kind == ast.KindLiteralType && parent.Parent.Kind == ast.KindImportType && parent.Parent.AsImportTypeNode().Qualifier == nil:
			edges.whole = append(edges.whole, path)
		case parent.Kind == ast.KindExportDeclaration && parent.AsExportDeclaration().ExportClause == nil:
			edges.star = append(edges.star, path)
		}
	}
	return edges
}

// hasModuleAugmentation reports whether a file declares global or module
// augmentations, which take effect without the file being imported.
func hasModuleAugmentation(sf *ast.SourceFile) bool {
	for _, statement := range sf.Statements.Nodes {
		if statement.Kind == ast.KindModuleDeclaration && hasModifier(statement, ast.KindDeclareKeyword) {
			return true
		}
	}
	return false
}

// collectExportCandidates returns the file's named exports, one per name,
// and maps each of their declarations (overloads and merged declarations
// included) to it.
func collectExportCandidates(file *lint.File, byDecl map[declKey]*exportCandidate) []*exportCandidate {
	var candidates []*exportCandidate
	byName := map[string]*exportCandidate{}
	add := func(name string, node, declaration *ast.Node) {
		c := byName[name]
		if c == nil {
			c = &exportCandidate{file: file, name: name, node: node}
			byName[name] = c
			candidates = append(candidates, c)
		}
		c.declarations = append(c.declarations, declaration)
		byDecl[keyOfDeclaration(declaration)] = c
	}
	var addBindings func(name, declaration *ast.Node)
	addBindings = func(name, declaration *ast.Node) {
		if name.Kind == ast.KindIdentifier {
			add(name.Text(), name, declaration)
			return
		}
		for _, element := range name.AsBindingPattern().Elements.Nodes {
			if element.Kind == ast.KindBindingElement {
				addBindings(element.Name(), element)
			}
		}
	}
	for _, statement := range file.Source.Statements.Nodes {
		switch statement.Kind {
		case ast.KindVariableStatement:
			if hasModifier(statement, ast.KindExportKeyword) {
				for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
					addBindings(declaration.Name(), declaration)
				}
			}
		case ast.KindFunctionDeclaration, ast.KindClassDeclaration, ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration,
			ast.KindEnumDeclaration, ast.KindModuleDeclaration:
			name := statement.Name()
			switch {
			case !hasModifier(statement, ast.KindExportKeyword):
			case hasModifier(statement, ast.KindDefaultKeyword):
				add("default", nameOr(name, statement), statement)
			case name != nil && name.Kind == ast.KindIdentifier:
				add(name.Text(), name, statement)
			}
		case ast.KindExportDeclaration:
			clause := statement.AsExportDeclaration().ExportClause
			switch {
			case clause == nil:
			case clause.Kind == ast.KindNamespaceExport:
				add(clause.Name().Text(), clause.Name(), clause)
			default:
				for _, specifier := range clause.AsNamedExports().Elements.Nodes {
					add(specifier.Name().Text(), specifier.Name(), specifier)
				}
			}
		case ast.KindExportAssignment:
			if !statement.AsExportAssignment().IsExportEquals {
				add("default", statement, statement)
			}
		}
	}
	return candidates
}

// nameOr returns name, or node when it has none.
func nameOr(name, node *ast.Node) *ast.Node {
	if name != nil {
		return name
	}
	return node
}

// exportUses records the uses of export candidates in one file.
type exportUses struct {
	ch          *checker.Checker
	file        *lint.File
	byDecl      map[declKey]*exportCandidate
	wholeModule map[tspath.Path]*atomic.Bool
	// exportsEscape is set for entry points, whose exports of a namespace
	// expose all of its members.
	exportsEscape bool
	// localTargets maps the local declarations that the file's own export
	// specifiers export to their candidates, so that local uses count as uses
	// in the file.
	localTargets map[declKey]*exportCandidate
}

func (u *exportUses) collect(names map[string]bool) {
	sf := u.file.Source
	// Local names of imports: namespace and renamed imports are used by them.
	local := map[string]bool{}
	for _, statement := range sf.Statements.Nodes {
		switch statement.Kind {
		case ast.KindImportDeclaration:
			clause := statement.AsImportDeclaration().ImportClause
			if clause == nil {
				continue
			}
			if clause.Name() != nil {
				local[clause.Name().Text()] = true
			}
			if bindings := clause.AsImportClause().NamedBindings; bindings != nil {
				if bindings.Kind == ast.KindNamespaceImport {
					local[bindings.Name().Text()] = true
				} else {
					for _, specifier := range bindings.AsNamedImports().Elements.Nodes {
						local[specifier.Name().Text()] = true
					}
				}
			}
		case ast.KindExportDeclaration:
			declaration := statement.AsExportDeclaration()
			if declaration.ModuleSpecifier != nil || declaration.ExportClause == nil || declaration.ExportClause.Kind != ast.KindNamedExports {
				continue
			}
			for _, specifier := range declaration.ExportClause.AsNamedExports().Elements.Nodes {
				c := u.byDecl[keyOfDeclaration(specifier)]
				target := u.ch.GetExportSpecifierLocalTargetSymbol(specifier)
				if c == nil || target == nil {
					continue
				}
				if u.localTargets == nil {
					u.localTargets = map[declKey]*exportCandidate{}
				}
				for _, d := range target.Declarations {
					u.localTargets[keyOfDeclaration(d)] = c
				}
			}
		}
	}

	forEachNode(sf.AsNode(), func(node *ast.Node) {
		if node.Kind != ast.KindIdentifier {
			return
		}
		parent := node.Parent
		switch parent.Kind {
		case ast.KindImportSpecifier, ast.KindImportClause, ast.KindNamespaceImport, ast.KindImportEqualsDeclaration:
			// An import uses what it imports, whatever its local name.
			if parent.Name() == node {
				if symbol := u.ch.GetSymbolAtLocation(node); symbol != nil {
					u.markChain(u.ch.GetImmediateAliasedSymbol(symbol), node, false)
				}
			}
			return
		case ast.KindExportSpecifier, ast.KindNamespaceExport:
			// So does a re-export; the export itself isn't a use of it.
			if parent.Name() == node {
				if symbol := u.ch.GetSymbolAtLocation(node); symbol != nil {
					u.markChain(u.ch.GetImmediateAliasedSymbol(symbol), node, u.exportsEscape)
				}
			}
			return
		case ast.KindShorthandPropertyAssignment:
			if names[node.Text()] || local[node.Text()] {
				u.markChain(u.ch.GetShorthandAssignmentValueSymbol(parent), node, true)
			}
			return
		}
		if !names[node.Text()] && !local[node.Text()] || ast.IsDeclarationName(node) {
			return
		}
		u.markChain(u.ch.GetSymbolAtLocation(node), node, !isMemberAccessTarget(node))
	})
}

// markChain marks the candidates among the declarations of symbol and of
// the symbols it aliases, in turn, as used at node. A chain that ends at a
// module (a namespace import) uses all of its exports when escapes is set:
// the namespace is used as a value, not only for its members.
func (u *exportUses) markChain(symbol *ast.Symbol, node *ast.Node, escapes bool) {
	for hops := 0; symbol != nil && hops < 32; hops++ {
		for _, d := range symbol.Declarations {
			key := keyOfDeclaration(d)
			if c := u.byDecl[key]; c != nil {
				c.use(node)
			}
			if c := u.localTargets[key]; c != nil && !contains(d, node) {
				c.usedInFile.Store(true)
			}
			if d.Kind == ast.KindSourceFile && escapes {
				if used := u.wholeModule[d.AsSourceFile().Path()]; used != nil {
					used.Store(true)
				}
			}
		}
		if symbol.Flags&ast.SymbolFlagsAlias == 0 {
			return
		}
		symbol = u.ch.GetImmediateAliasedSymbol(symbol)
	}
}

// use records a use of the candidate at node. Uses inside its own
// declarations, like recursion, don't count.
func (c *exportCandidate) use(node *ast.Node) {
	if pathOf(node) != c.file.Source.Path() {
		c.usedOutside.Store(true)
		return
	}
	for _, declaration := range c.declarations {
		if contains(declaration, node) {
			return
		}
	}
	c.usedInFile.Store(true)
}

func contains(outer, node *ast.Node) bool {
	return pathOf(outer) == pathOf(node) && outer.Pos() <= node.Pos() && node.End() <= outer.End()
}

// isMemberAccessTarget reports whether node is the namespace in a member
// access: ns.member in an expression or a type.
func isMemberAccessTarget(node *ast.Node) bool {
	parent := node.Parent
	switch parent.Kind {
	case ast.KindPropertyAccessExpression:
		return parent.Expression() == node
	case ast.KindQualifiedName:
		return parent.AsQualifiedName().Left == node
	}
	return false
}
