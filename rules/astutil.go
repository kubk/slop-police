package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/tspath"
)

// forEachNode walks root and its descendants in forEachChild order (pre-order).
func forEachNode(root *ast.Node, fn func(node *ast.Node)) {
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		fn(node)
		node.ForEachChild(visit)
		return false
	}
	visit(root)
}

// resolveSymbol is getSymbolAtLocation, followed by getAliasedSymbol for an
// alias symbol.
func resolveSymbol(ch *checker.Checker, node *ast.Node) *ast.Symbol {
	symbol := ch.GetSymbolAtLocation(node)
	if symbol != nil && symbol.Flags&ast.SymbolFlagsAlias != 0 {
		symbol = ch.GetAliasedSymbol(symbol)
	}
	return symbol
}

// staticName returns the text of a name that is known without evaluating
// it: an identifier, string, number or template without substitutions.
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

// unwrapExpression strips parentheses, `as`, `satisfies` and non-null
// assertions.
func unwrapExpression(node *ast.Node) *ast.Node {
	for {
		switch node.Kind {
		case ast.KindParenthesizedExpression, ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindNonNullExpression:
			node = node.Expression()
		default:
			return node
		}
	}
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

// declKey identifies a declaration across parses of its file: its file's
// path and position.
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
