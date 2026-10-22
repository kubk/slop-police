package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

func init() {
	lint.Register(&lint.Rule{
		Name:  "unused-object-keys",
		Check: checkUnusedObjectKeys,
	})
}

type objectKey struct {
	name string
	node *ast.Node
}

type objectCandidate struct {
	file           *lint.File
	name           string
	keys           []objectKey
	usedKeys       map[string]bool
	escaped        bool
	referenceCount int
}

type objectKeyUse struct {
	candidate int
	keys      []string
	escape    bool
}

func checkUnusedObjectKeys(ctx *lint.Context, _ lint.Options) []lint.Finding {
	candidates, byDecl := collectObjectCandidates(ctx)
	if len(candidates) == 0 {
		return nil
	}
	names := map[string]bool{}
	for _, c := range candidates {
		names[c.name] = true
	}

	escapedNames := make([][]string, len(ctx.Files))
	uses := make([][]objectKeyUse, len(ctx.Files))
	ctx.ForEachFile(ctx.Files, func(file *lint.File, ch *checker.Checker) {
		var locations []*ast.Node
		Visit(file.Source.AsNode(), func(node *ast.Node) {
			if node.Kind != ast.KindIdentifier || !names[node.Text()] {
				return
			}
			parent := node.Parent
			switch parent.Kind {
			case ast.KindVariableDeclaration, ast.KindPropertyAssignment:
				if parent.Name() == node {
					return
				}
			case ast.KindImportSpecifier, ast.KindExportSpecifier, ast.KindImportClause, ast.KindNamespaceImport:
				return
			case ast.KindShorthandPropertyAssignment:
				escapedNames[file.Index] = append(escapedNames[file.Index], node.Text())
				return
			}
			locations = append(locations, node)
		})
		for _, location := range locations {
			symbol := lint.ResolveSymbol(ch, location, true)
			candidate := findObjectCandidate(ctx, byDecl, symbol)
			if candidate < 0 {
				continue
			}
			keys, ok := staticallyReadKeys(location)
			uses[file.Index] = append(uses[file.Index], objectKeyUse{candidate: candidate, keys: keys, escape: !ok})
		}
	})

	for i := range ctx.Files {
		for _, name := range escapedNames[i] {
			for _, c := range candidates {
				if c.name == name {
					c.escaped = true
				}
			}
		}
		for _, use := range uses[i] {
			c := candidates[use.candidate]
			c.referenceCount++
			if use.escape {
				c.escaped = true
				continue
			}
			for _, key := range use.keys {
				c.usedKeys[key] = true
			}
		}
	}

	var findings []lint.Finding
	for _, c := range candidates {
		if c.escaped || c.referenceCount == 0 {
			continue
		}
		for _, key := range c.keys {
			if c.usedKeys[key.name] {
				continue
			}
			findings = append(findings, lint.Finding{File: c.file, Node: key.node, Message: c.name + "." + key.name + " is never read"})
		}
	}
	return findings
}

func collectObjectCandidates(ctx *lint.Context) ([]*objectCandidate, map[declKey]int) {
	var candidates []*objectCandidate
	byDecl := map[declKey]int{}
	for _, file := range ctx.Files {
		if file.IsTest {
			continue
		}
		for _, statement := range file.Source.Statements.Nodes {
			if statement.Kind != ast.KindVariableStatement {
				continue
			}
			list := statement.AsVariableStatement().DeclarationList
			if list.Flags&ast.NodeFlagsConst == 0 {
				continue
			}
			for _, declaration := range list.AsVariableDeclarationList().Declarations.Nodes {
				decl := declaration.AsVariableDeclaration()
				name := declaration.Name()
				if name.Kind != ast.KindIdentifier || decl.Type != nil {
					continue
				}
				object := unwrapAsConst(decl.Initializer)
				if object == nil || containsThisKeyword(object) {
					continue
				}
				var keys []objectKey
				for _, property := range object.AsObjectLiteralExpression().Properties.Nodes {
					if key, ok := staticPropertyName(property); ok {
						keys = append(keys, objectKey{name: key, node: property.Name()})
					}
				}
				if len(keys) == 0 {
					continue
				}
				key := keyOfDeclaration(declaration)
				if existing, ok := byDecl[key]; ok {
					candidates[existing] = &objectCandidate{file: file, name: name.Text(), keys: keys, usedKeys: map[string]bool{}}
					continue
				}
				byDecl[key] = len(candidates)
				candidates = append(candidates, &objectCandidate{file: file, name: name.Text(), keys: keys, usedKeys: map[string]bool{}})
			}
		}
	}
	return candidates, byDecl
}

func findObjectCandidate(ctx *lint.Context, byDecl map[declKey]int, symbol *ast.Symbol) int {
	if symbol == nil {
		return -1
	}
	for _, declaration := range symbol.Declarations {
		if !ctx.IsAnalyzedPath(pathOf(declaration)) {
			continue
		}
		if index, ok := byDecl[keyOfDeclaration(declaration)]; ok {
			return index
		}
	}
	return -1
}

func staticallyReadKeys(identifier *ast.Node) ([]string, bool) {
	expression := identifier
	if identifier.Parent.Kind == ast.KindPropertyAccessExpression && identifier.Parent.Name() == identifier {
		expression = identifier.Parent
	}
	for expression.Parent.Kind == ast.KindParenthesizedExpression || expression.Parent.Kind == ast.KindNonNullExpression {
		expression = expression.Parent
	}
	parent := expression.Parent
	switch parent.Kind {
	case ast.KindPropertyAccessExpression:
		access := parent.AsPropertyAccessExpression()
		if access.Expression == expression {
			return []string{parent.Name().Text()}, true
		}
	case ast.KindElementAccessExpression:
		access := parent.AsElementAccessExpression()
		if access.Expression == expression {
			argument := access.ArgumentExpression
			switch argument.Kind {
			case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral, ast.KindNumericLiteral:
				return []string{argument.Text()}, true
			}
			return nil, false
		}
	case ast.KindVariableDeclaration:
		decl := parent.AsVariableDeclaration()
		if decl.Initializer == expression && parent.Name().Kind == ast.KindObjectBindingPattern {
			keys := []string{}
			for _, element := range parent.Name().AsBindingPattern().Elements.Nodes {
				binding := element.AsBindingElement()
				if binding.DotDotDotToken != nil {
					return nil, false
				}
				nameNode := binding.PropertyName
				if nameNode == nil {
					nameNode = element.Name()
				}
				key, ok := staticName(nameNode)
				if !ok {
					return nil, false
				}
				keys = append(keys, key)
			}
			return keys, true
		}
	}
	return nil, false
}

func unwrapAsConst(node *ast.Node) *ast.Node {
	for node != nil {
		switch node.Kind {
		case ast.KindObjectLiteralExpression:
			return node
		case ast.KindParenthesizedExpression:
			node = node.AsParenthesizedExpression().Expression
		case ast.KindAsExpression:
			as := node.AsAsExpression()
			if !isConstTypeReference(as.Type) {
				return nil
			}
			node = as.Expression
		default:
			return nil
		}
	}
	return nil
}

func isConstTypeReference(typeNode *ast.Node) bool {
	if typeNode == nil || typeNode.Kind != ast.KindTypeReference {
		return false
	}
	name := typeNode.AsTypeReferenceNode().TypeName
	return name.Kind == ast.KindIdentifier && name.Text() == "const"
}

func containsThisKeyword(node *ast.Node) bool {
	found := false
	Visit(node, func(child *ast.Node) {
		if child.Kind == ast.KindThisKeyword {
			found = true
		}
	})
	return found
}

func staticPropertyName(property *ast.Node) (string, bool) {
	switch property.Kind {
	case ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment, ast.KindMethodDeclaration,
		ast.KindGetAccessor, ast.KindSetAccessor:
		return staticName(property.Name())
	}
	return "", false
}
