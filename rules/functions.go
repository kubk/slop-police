package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// A checkedFunction is a function the function-shape rules look at: a
// function or method declaration, or a function expression or arrow function
// that initializes a variable, with a block body.
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
