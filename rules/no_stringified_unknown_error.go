package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// no-stringified-unknown-error reports `e instanceof Error ? e.message :
// String(e)` (either branch order), which flattens a caught value into a
// string and drops its type, stack and cause. A plain instanceof check only
// counts for the usual error variable names, while an `||` of instanceof
// checks allows any identifiers on their left sides.
func init() {
	lint.Register(&lint.Rule{
		Name: "no-stringified-unknown-error",
		Visit: func(file *lint.File, node *ast.Node, _ lint.Options, report lint.Report) {
			if node.Kind != ast.KindConditionalExpression {
				return
			}
			if isWholeFunctionResult(node) {
				return
			}
			if name := stringifiedErrorName(node.AsConditionalExpression()); name != "" {
				report(node, "Caught value "+name+" is flattened into a string")
			}
		},
	})
}

// isWholeFunctionResult reports whether node is all a function does, as in
// getErrorMessage(error) { return error instanceof Error ? ... }. Such a
// helper is where the conversion belongs, instead of repeating it inline.
func isWholeFunctionResult(node *ast.Node) bool {
	parent := node.Parent
	for parent.Kind == ast.KindParenthesizedExpression {
		parent = parent.Parent
	}
	if parent.Kind == ast.KindArrowFunction {
		return true
	}
	if parent.Kind != ast.KindReturnStatement || parent.Parent.Kind != ast.KindBlock {
		return false
	}
	block := parent.Parent
	return len(block.Statements()) == 1 && isFunctionLikeDeclaration(block.Parent)
}

var errorVariableNames = map[string]bool{"error": true, "err": true, "e": true, "cause": true}

// stringifiedErrorName returns the name whose .message the conditional
// reads, or "" when it isn't the pattern.
func stringifiedErrorName(conditional *ast.ConditionalExpression) string {
	names := map[string]bool{}
	condition := unwrapExpression(conditional.Condition)
	if isBinary(condition, ast.KindBarBarToken) {
		binary := condition.AsBinaryExpression()
		for _, side := range []*ast.Node{binary.Left, binary.Right} {
			side = unwrapExpression(side)
			if isBinary(side, ast.KindInstanceOfKeyword) {
				if left := unwrapExpression(side.AsBinaryExpression().Left); left.Kind == ast.KindIdentifier {
					names[left.Text()] = true
				}
			}
		}
	} else if isBinary(condition, ast.KindInstanceOfKeyword) {
		binary := condition.AsBinaryExpression()
		right := unwrapExpression(binary.Right)
		if name, ok := identifierIn(binary.Left, errorVariableNames); ok && right.Kind == ast.KindIdentifier && right.Text() == "Error" {
			names[name] = true
		}
	}
	if len(names) == 0 {
		return ""
	}
	whenTrue, whenFalse := conditional.WhenTrue, conditional.WhenFalse
	if name, ok := errorMessageName(whenTrue, names); ok && isStringCallOf(whenFalse, names) {
		return name
	}
	if name, ok := errorMessageName(whenFalse, names); ok && isStringCallOf(whenTrue, names) {
		return name
	}
	return ""
}

func isBinary(node *ast.Node, operator ast.Kind) bool {
	return node.Kind == ast.KindBinaryExpression && node.AsBinaryExpression().OperatorToken.Kind == operator
}

// identifierIn returns the name of expression when it's an identifier (once
// unwrapped) whose name is in names.
func identifierIn(expression *ast.Node, names map[string]bool) (string, bool) {
	expression = unwrapExpression(expression)
	if expression.Kind == ast.KindIdentifier && names[expression.Text()] {
		return expression.Text(), true
	}
	return "", false
}

// errorMessageName matches `name.message`.
func errorMessageName(expression *ast.Node, names map[string]bool) (string, bool) {
	expression = unwrapExpression(expression)
	if expression.Kind != ast.KindPropertyAccessExpression || expression.Name().Text() != "message" {
		return "", false
	}
	return identifierIn(expression.Expression(), names)
}

// isStringCallOf matches `String(name)`.
func isStringCallOf(expression *ast.Node, names map[string]bool) bool {
	expression = unwrapExpression(expression)
	if expression.Kind != ast.KindCallExpression {
		return false
	}
	call := expression.AsCallExpression()
	if call.Expression.Kind != ast.KindIdentifier || call.Expression.Text() != "String" || len(nodeList(call.Arguments)) != 1 {
		return false
	}
	_, ok := identifierIn(call.Arguments.Nodes[0], names)
	return ok
}
