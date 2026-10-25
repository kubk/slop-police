package rules

import (
	"slices"
	"sync/atomic"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// unused-enum-members reports enum members that nothing reads, as long as
// every use of the enum names its members (E.A, E["A"], the type E.A) or
// uses it as a type. Enums used any other way (Object.values(E), passed to a
// function, keyof typeof E, exported under another name) are skipped: their
// members may be read without being named.
func init() {
	lint.Register(&lint.Rule{
		Name:  "unused-enum-members",
		Check: checkUnusedEnumMembers,
	})
}

type enumCandidate struct {
	file    *lint.File
	name    string
	members []*ast.Node
	used    []atomic.Bool
	escaped atomic.Bool
}

func checkUnusedEnumMembers(ctx *lint.Context, _ lint.Options) []lint.Finding {
	var candidates []*enumCandidate
	byEnum := map[declKey]*enumCandidate{}
	type memberRef struct {
		enum  *enumCandidate
		index int
	}
	byMember := map[declKey]memberRef{}
	names := map[string]bool{}
	for _, file := range ctx.Files {
		if file.IsTest {
			continue
		}
		forEachNode(file.Source.AsNode(), func(node *ast.Node) {
			if node.Kind != ast.KindEnumDeclaration {
				return
			}
			members := node.AsEnumDeclaration().Members.Nodes
			c := &enumCandidate{file: file, name: node.Name().Text(), members: members, used: make([]atomic.Bool, len(members))}
			candidates = append(candidates, c)
			byEnum[keyOfDeclaration(node)] = c
			names[c.name] = true
			for i, member := range members {
				byMember[keyOfDeclaration(member)] = memberRef{c, i}
				if name, ok := staticName(member.Name()); ok {
					names[name] = true
				}
			}
		})
	}
	if len(candidates) == 0 {
		return nil
	}

	files := append(slices.Clip(ctx.Files), ctx.Unanalyzed...)
	ctx.ForEachFile(files, func(file *lint.File, ch *checker.Checker) {
		// Renamed imports use the enum by their local name.
		local := map[string]bool{}
		forEachNode(file.Source.AsNode(), func(node *ast.Node) {
			if node.Kind == ast.KindImportSpecifier && node.PropertyName() != nil && names[node.PropertyName().Text()] {
				local[node.Name().Text()] = true
			}
		})
		forEachNode(file.Source.AsNode(), func(node *ast.Node) {
			if node.Kind != ast.KindIdentifier && node.Kind != ast.KindStringLiteral || !names[node.Text()] && !local[node.Text()] {
				return
			}
			symbol := ch.GetSymbolAtLocation(node)
			if symbol == nil {
				return
			}
			for _, d := range symbol.Declarations {
				if ref, ok := byMember[keyOfDeclaration(d)]; ok && d.Name() != node {
					ref.enum.used[ref.index].Store(true)
				}
			}
			if symbol.Flags&ast.SymbolFlagsAlias != 0 {
				symbol = ch.GetAliasedSymbol(symbol)
			}
			if node.Kind == ast.KindIdentifier && enumUseEscapes(node) {
				for _, d := range symbol.Declarations {
					if c := byEnum[keyOfDeclaration(d)]; c != nil {
						c.escaped.Store(true)
					}
				}
			}
		})
	})

	var findings []lint.Finding
	for _, c := range candidates {
		if c.escaped.Load() {
			continue
		}
		for i, member := range c.members {
			if !c.used[i].Load() {
				name, _ := staticName(member.Name())
				findings = append(findings, lint.Finding{File: c.file, Node: member.Name(), Message: "Enum member " + c.name + "." + name + " is never used"})
			}
		}
	}
	return findings
}

// enumUseEscapes reports whether an identifier that may name an enum uses
// it other than by naming a member or as a type.
func enumUseEscapes(node *ast.Node) bool {
	expression := node
	parent := node.Parent
	switch parent.Kind {
	case ast.KindEnumDeclaration, ast.KindEnumMember, ast.KindImportSpecifier, ast.KindImportClause, ast.KindTypeReference:
		return false
	case ast.KindExportSpecifier:
		// Exported under another name, its uses can't be followed by name.
		return parent.PropertyName() != nil
	case ast.KindQualifiedName:
		if parent.AsQualifiedName().Left == node {
			return false
		}
		expression = parent
	case ast.KindPropertyAccessExpression:
		if parent.Name() == node {
			expression = parent // ns.E
		}
	}
	parent = expression.Parent
	switch parent.Kind {
	case ast.KindPropertyAccessExpression:
		return parent.Expression() != expression
	case ast.KindElementAccessExpression:
		access := parent.AsElementAccessExpression()
		return access.Expression != expression || !ast.IsStringLiteralLike(access.ArgumentExpression)
	case ast.KindQualifiedName:
		return parent.AsQualifiedName().Left != expression
	case ast.KindTypeReference:
		return false
	}
	return true
}
