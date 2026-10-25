package rules

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

func init() {
	lint.Register(&lint.Rule{
		Name: "no-pass-through-wrapper",
		CheckFile: func(file *lint.File, ch *checker.Checker, _ lint.Options, report lint.Report) {
			forEachNode(file.Source.AsNode(), func(node *ast.Node) {
				fn, ok := functionAt(node)
				if !ok {
					return
				}
				call := passThroughCall(fn)
				if call == nil || !isRedundantOverride(fn, call) && (isShapeRequired(ch, node, fn) || addsTypes(ch, fn, call) || isPrivateMemberDelegation(ch, call)) {
					return
				}
				callee := strings.Join(strings.Fields(lint.Text(call.Expression(), file.Source)), "")
				report(fn.Name, fn.Label(file)+" only forwards to "+callee)
			})
		},
	})
}

// passThroughCall returns the call in the body of fn that is a single
// `return f(...)` or `return await f(...)` statement, when the callee is a
// plain path (see isPlainPath) and every argument is one of the function's
// parameters, as is: the function only forwards to another one.
func passThroughCall(fn checkedFunction) *ast.Node {
	statements := fn.Body.Statements()
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
	for _, parameter := range fn.Function.Parameters() {
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

// isRedundantOverride reports whether a method only calls the method it
// overrides, as in close() { return super.close(); }: the class behaves the
// same without it, whatever the types say.
func isRedundantOverride(fn checkedFunction, call *ast.Node) bool {
	callee := unwrapParentheses(call.Expression())
	if fn.Function.Kind != ast.KindMethodDeclaration || callee.Kind != ast.KindPropertyAccessExpression || callee.Expression().Kind != ast.KindSuperKeyword {
		return false
	}
	name, ok := staticName(fn.Name)
	return ok && name == callee.AsPropertyAccessExpression().Name().Text()
}

// isShapeRequired reports whether the wrapper's name and signature are
// dictated by a type it has to fit: a method of a contextually typed object
// literal (a Proxy handler, a Worker's fetch), a class method that overrides
// or implements an inherited member, or a function stored in a variable
// with a declared type.
func isShapeRequired(ch *checker.Checker, declaration *ast.Node, fn checkedFunction) bool {
	if declaration.Kind == ast.KindVariableDeclaration {
		return declaration.Type() != nil
	}
	if declaration.Kind != ast.KindMethodDeclaration {
		return false
	}
	parent := declaration.Parent
	if parent.Kind == ast.KindObjectLiteralExpression {
		return ch.GetContextualTypeForObjectLiteralElement(declaration, checker.ContextFlagsNone) != nil
	}
	if !isClassLike(parent) || hasModifier(declaration, ast.KindStaticKeyword) {
		return false
	}
	if hasModifier(declaration, ast.KindOverrideKeyword) {
		return true
	}
	name, ok := staticName(fn.Name)
	if !ok {
		return false
	}
	var inherited []*checker.Type
	for _, typeNode := range implementedTypes(parent) {
		inherited = append(inherited, ch.GetTypeAtLocation(typeNode))
	}
	if symbol := parent.Symbol(); symbol != nil {
		inherited = append(inherited, ch.GetBaseTypes(ch.GetDeclaredTypeOfSymbol(symbol))...)
	}
	for _, t := range inherited {
		if ch.GetPropertyOfType(t, name) != nil {
			return true
		}
	}
	return false
}

// addsTypes reports whether the wrapper gives the forwarded call a different
// type surface: type parameters, overloads, a parameter declared with another
// type than the callee takes there, or a declared return type other than the
// call's. Declaring any only drops types, so it doesn't count.
func addsTypes(ch *checker.Checker, fn checkedFunction, call *ast.Node) bool {
	if len(fn.Function.TypeParameters()) > 0 {
		return true
	}
	if symbol := fn.Function.Symbol(); symbol != nil && len(symbol.Declarations) > 1 {
		return true
	}
	declaredTypes := map[string]*ast.Node{}
	for _, parameter := range fn.Function.Parameters() {
		if name := parameter.Name(); name.Kind == ast.KindIdentifier && parameter.Type() != nil {
			declaredTypes[name.Text()] = parameter
		}
	}
	for i, argument := range call.Arguments() {
		parameter := declaredTypes[argument.Text()]
		if parameter == nil {
			continue
		}
		if declared := ch.GetTypeAtLocation(parameter.Name()); !isAny(declared) && !sameType(ch, declared, ch.GetContextualTypeForArgumentAtIndex(call, i)) {
			return true
		}
	}
	if returnType := fn.Function.Type(); returnType != nil {
		// A type predicate's type is boolean, but it narrows the argument.
		if returnType.Kind == ast.KindTypePredicate {
			return true
		}
		declared := ch.GetTypeFromTypeNode(returnType)
		return !isAny(declared) && !sameType(ch, declared, ch.GetTypeAtLocation(call))
	}
	return false
}

func isAny(t *checker.Type) bool {
	return t != nil && t.Flags()&checker.TypeFlagsAny != 0
}

// sameType approximates type identity (which the checker doesn't export) as
// assignability both ways. any is assignable both ways to everything, so it
// only counts as the same as any.
func sameType(ch *checker.Checker, a, b *checker.Type) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil || isAny(a) || isAny(b) {
		return false
	}
	return ch.IsTypeAssignableTo(a, b) && ch.IsTypeAssignableTo(b, a)
}

// isPrivateMemberDelegation reports whether call is this.member.method(...)
// on a private or protected member: the wrapper is the class's API over its
// own internals, like get(key) { return this.items.get(key) }.
func isPrivateMemberDelegation(ch *checker.Checker, call *ast.Node) bool {
	callee := unwrapParentheses(call.Expression())
	if callee.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	member := unwrapParentheses(callee.Expression())
	if member.Kind != ast.KindPropertyAccessExpression || member.Expression().Kind != ast.KindThisKeyword {
		return false
	}
	name := member.AsPropertyAccessExpression().Name()
	if name.Kind == ast.KindPrivateIdentifier {
		return true
	}
	symbol := ch.GetSymbolAtLocation(name)
	return symbol != nil && checker.GetDeclarationModifierFlagsFromSymbol(symbol)&(ast.ModifierFlagsPrivate|ast.ModifierFlagsProtected) != 0
}
