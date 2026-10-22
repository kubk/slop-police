package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/tspath"
)

// Visit walks node and its descendants in forEachChild order (pre-order).
func Visit(root *ast.Node, fn func(node *ast.Node)) {
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		fn(node)
		node.ForEachChild(visit)
		return false
	}
	visit(root)
}

// staticName mirrors ast.mjs getStaticName.
func staticName(name *ast.Node) (string, bool) {
	if name == nil {
		return "", false
	}
	switch name.Kind {
	case ast.KindIdentifier, ast.KindStringLiteral, ast.KindNumericLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return name.Text(), true
	}
	return "", false
}

func unwrapParentheses(node *ast.Node) *ast.Node {
	for node != nil && node.Kind == ast.KindParenthesizedExpression {
		node = node.AsParenthesizedExpression().Expression
	}
	return node
}

func isFunctionLikeDeclaration(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindFunctionDeclaration, ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor,
		ast.KindConstructor, ast.KindFunctionExpression, ast.KindArrowFunction:
		return true
	}
	return false
}

func isClassLike(node *ast.Node) bool {
	return node != nil && (node.Kind == ast.KindClassDeclaration || node.Kind == ast.KindClassExpression)
}

func hasModifier(node *ast.Node, kind ast.Kind) bool {
	modifiers := node.Modifiers()
	if modifiers == nil {
		return false
	}
	for _, modifier := range modifiers.Nodes {
		if modifier.Kind == kind {
			return true
		}
	}
	return false
}

func pathOf(node *ast.Node) tspath.Path {
	return ast.GetSourceFileOfNode(node).Path()
}

// declKey mirrors context.declarationKey: `${path}:${node.pos}`.
type declKey struct {
	path tspath.Path
	pos  int
}

func keyOfDeclaration(node *ast.Node) declKey {
	return declKey{pathOf(node), node.Pos()}
}

func nodeList(list *ast.NodeList) []*ast.Node {
	if list == nil {
		return nil
	}
	return list.Nodes
}
