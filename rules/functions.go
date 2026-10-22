package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// A checkedFunction is a function the function-shape rules look at (the ones
// slop-scan summarizes): a function or method declaration, or a function
// expression or arrow function that initializes a variable, with a block body.
type checkedFunction struct {
	Function *ast.Node // the function-like node
	Name     *ast.Node // where to report: the declared or variable name, or the function itself when anonymous
	Body     *ast.Node // the block
}

// functionAt returns the checked function declared by node, if any. For a
// variable it's the variable declaration, not its initializer, so that each
// function is found exactly once.
func functionAt(node *ast.Node) (checkedFunction, bool) {
	fn, name := node, node.Name()
	switch node.Kind {
	case ast.KindFunctionDeclaration, ast.KindMethodDeclaration:
	case ast.KindVariableDeclaration:
		fn = node.Initializer()
		if fn == nil || fn.Kind != ast.KindArrowFunction && fn.Kind != ast.KindFunctionExpression {
			return checkedFunction{}, false
		}
	default:
		return checkedFunction{}, false
	}
	body := fn.Body()
	if body == nil || body.Kind != ast.KindBlock {
		return checkedFunction{}, false
	}
	if name == nil {
		name = fn
	}
	return checkedFunction{Function: fn, Name: name, Body: body}, true
}

// Label names the function in messages: "Function load", "Method load", or
// "Function" for an anonymous default export.
func (f checkedFunction) Label(file *lint.File) string {
	kind := "Function"
	if f.Function.Kind == ast.KindMethodDeclaration {
		kind = "Method"
	}
	if f.Name == f.Function {
		return kind
	}
	return kind + " " + lint.Text(f.Name, file.Source)
}

func (f checkedFunction) IsAsync() bool {
	return hasModifier(f.Function, ast.KindAsyncKeyword)
}

// PassThroughCall returns the call in a body that is a single
// `return f(...)` or `return await f(...)` statement, when the callee is a
// plain path (see isPlainPath) and every argument is one of the function's
// parameters, as is: the function only forwards to another one.
func (f checkedFunction) PassThroughCall() *ast.Node {
	statements := f.Body.Statements()
	if len(statements) != 1 || statements[0].Kind != ast.KindReturnStatement {
		return nil
	}
	call := statements[0].Expression()
	if call != nil && call.Kind == ast.KindAwaitExpression {
		call = call.Expression()
	}
	if call == nil || call.Kind != ast.KindCallExpression || !isPlainPath(call.Expression()) {
		return nil
	}
	parameters := map[string]bool{}
	for _, parameter := range f.Function.Parameters() {
		if name := parameter.Name(); name.Kind == ast.KindIdentifier {
			parameters[name.Text()] = true
		}
	}
	for _, argument := range call.Arguments() {
		if argument.Kind != ast.KindIdentifier || !parameters[argument.Text()] {
			return nil
		}
	}
	return call
}

// isPlainPath reports whether node only names something, like load,
// this.store.load or api["users"].load, without computing anything: no calls
// (x().y() does work before y), no `new`, no literal receivers.
func isPlainPath(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindIdentifier, ast.KindThisKeyword, ast.KindSuperKeyword:
		return true
	case ast.KindPropertyAccessExpression, ast.KindParenthesizedExpression, ast.KindNonNullExpression:
		return isPlainPath(node.Expression())
	case ast.KindElementAccessExpression:
		switch node.AsElementAccessExpression().ArgumentExpression.Kind {
		case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral, ast.KindNumericLiteral:
			return isPlainPath(node.Expression())
		}
	}
	return false
}
