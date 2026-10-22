package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// no-promise-default-fallback reports promise `.catch()` handlers that hide
// the rejection: an empty handler, or one that returns a cheap default
// (`null`, `undefined`, `false`, `0`, `""`, `[]`, `{}`) with nothing but
// console/logger calls before it. Matching follows slop-scan's
// defensive.promise-default-fallbacks, except that the receiver must be a
// promise or thenable (or `any`), so `.catch` methods of other objects don't
// count.
func init() {
	lint.Register(&lint.Rule{
		Name:      "no-promise-default-fallback",
		CheckFile: checkPromiseDefaultFallbacks,
	})
}

func checkPromiseDefaultFallbacks(file *lint.File, ch *checker.Checker, _ lint.Options, report lint.Report) {
	Visit(file.Source.AsNode(), func(node *ast.Node) {
		if node.Kind != ast.KindCallExpression {
			return
		}
		call := node.AsCallExpression()
		if call.Expression.Kind != ast.KindPropertyAccessExpression || call.Expression.Name().Text() != "catch" || len(nodeList(call.Arguments)) == 0 {
			return
		}
		handler := call.Arguments.Nodes[0]
		if handler.Kind != ast.KindArrowFunction && handler.Kind != ast.KindFunctionExpression {
			return
		}
		message := promiseFallbackMessage(file, handler)
		if message != "" && isThenable(ch, call.Expression.Expression()) {
			report(handler, message)
		}
	})
}

func promiseFallbackMessage(file *lint.File, handler *ast.Node) string {
	body := handler.Body()
	if body.Kind != ast.KindBlock {
		if fallback, ok := defaultLiteral(file, body); ok {
			return "Promise rejection is replaced with " + fallback
		}
		return ""
	}
	statements := body.Statements()
	if len(statements) == 0 {
		return "Promise rejection is swallowed by an empty catch handler"
	}
	var returned *ast.Node
	for _, statement := range statements {
		if statement.Kind == ast.KindReturnStatement {
			if returned != nil {
				return ""
			}
			returned = statement
		}
	}
	if returned == nil {
		return ""
	}
	fallback, ok := defaultLiteral(file, returned.Expression())
	if !ok {
		return ""
	}
	if len(statements) == 1 {
		return "Promise rejection is replaced with " + fallback
	}
	for _, statement := range statements {
		if statement != returned && !isLoggingStatement(file, statement) {
			return ""
		}
	}
	return "Promise rejection is logged and replaced with " + fallback
}

// defaultLiteral reports whether expression (nil for a bare `return;`) is a
// cheap default, and how to name it.
func defaultLiteral(file *lint.File, expression *ast.Node) (string, bool) {
	if expression == nil {
		return "undefined", true
	}
	expression = unwrapExpression(expression)
	switch expression.Kind {
	case ast.KindNullKeyword, ast.KindFalseKeyword:
		return lint.Text(expression, file.Source), true
	case ast.KindIdentifier:
		return "undefined", expression.Text() == "undefined"
	case ast.KindStringLiteral:
		return `""`, expression.Text() == ""
	case ast.KindNumericLiteral:
		return "0", expression.Text() == "0"
	case ast.KindArrayLiteralExpression:
		return "[]", len(expression.AsArrayLiteralExpression().Elements.Nodes) == 0
	case ast.KindObjectLiteralExpression:
		return "{}", len(expression.AsObjectLiteralExpression().Properties.Nodes) == 0
	}
	return "", false
}

// isLoggingStatement matches `console.x(...)` and `logger.x(...)` statements.
func isLoggingStatement(file *lint.File, statement *ast.Node) bool {
	if statement.Kind != ast.KindExpressionStatement {
		return false
	}
	expression := statement.Expression()
	if expression.Kind != ast.KindCallExpression {
		return false
	}
	callee := expression.Expression()
	if callee.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	target := lint.Text(callee.Expression(), file.Source)
	return target == "console" || target == "logger"
}

// isThenable reports whether the type of expression (without null and
// undefined, for `?.catch`) has a callable `then`. An `any` receiver counts:
// nothing says it isn't a promise.
func isThenable(ch *checker.Checker, expression *ast.Node) bool {
	t := ch.GetNonNullableType(ch.GetTypeAtLocation(expression))
	if t.Flags()&checker.TypeFlagsAny != 0 {
		return true
	}
	then := ch.GetPropertyOfType(t, "then")
	if then == nil {
		return false
	}
	thenType := ch.GetNonNullableType(ch.GetTypeOfSymbol(then))
	return len(ch.GetSignaturesOfType(thenType, checker.SignatureKindCall)) > 0
}
