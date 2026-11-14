package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
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

// forEachNodeAndJSDoc walks the file like forEachNode, and the JSDoc comments
// of its nodes: the names in {@link a.b}, @see a.b and @type {a["b"]}.
func forEachNodeAndJSDoc(source *ast.SourceFile, fn func(node *ast.Node)) {
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		fn(node)
		ast.ForEachChildAndJSDoc(node, source, visit)
		return false
	}
	visit(source.AsNode())
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

// moduleObjectAliases returns the names under which the module objects that
// may be used as values (see lint.Context.HasModuleObjectValue) export what
// is declared under another name or in another file: with an export list,
// export * as or export default of an expression.
func moduleObjectAliases(ctx *lint.Context) map[string]bool {
	names := map[string]bool{}
	for _, files := range [][]*lint.File{ctx.Files, ctx.Unanalyzed} {
		for _, file := range files {
			if !ctx.HasModuleObjectValue(file) {
				continue
			}
			for _, c := range collectExportCandidates(file, map[declKey]*exportCandidate{}) {
				switch c.declarations[0].Kind {
				case ast.KindExportSpecifier, ast.KindNamespaceExport, ast.KindExportAssignment:
					names[c.name] = true
				}
			}
		}
	}
	return names
}

// isValueMember reports whether name is read as a member of a value, as in
// value.name, and not of a namespace or an enum, as in ns.name. The checker
// resolves it from the type of the value, which is slow to work out, and it
// is a top-level declaration of a module only when the value is a module
// object. So the rules that look for uses of such declarations skip these
// names, except the ones that a module object used as a value can have: the
// moduleObjectAliases and the names of the rule's candidates in the files
// with such an object.
func isValueMember(ch *checker.Checker, name *ast.Node) bool {
	owner := memberOwner(name)
	return owner != nil && !startsAtNamespace(ch, owner)
}

// memberOwner returns what name is read from as a member, when the checker
// resolves name from its type: x for x.name, typeof x.name and x["name"],
// the pattern for const { name: y } = x. It returns nil for other nodes,
// which resolve from scopes, imports and declarations.
func memberOwner(name *ast.Node) *ast.Node {
	parent := name.Parent
	switch parent.Kind {
	case ast.KindPropertyAccessExpression:
		if parent.Name() == name {
			return parent.Expression()
		}
	case ast.KindQualifiedName:
		// In a type, like ns.Type, a qualified name resolves from the names.
		if parent.AsQualifiedName().Right == name && ast.IsExpressionNode(parent) {
			return parent.AsQualifiedName().Left
		}
	case ast.KindElementAccessExpression:
		// In x[name], name is a variable, not a member.
		if parent.AsElementAccessExpression().ArgumentExpression == name && name.Kind != ast.KindIdentifier {
			return parent.Expression()
		}
	case ast.KindBindingElement:
		if parent.PropertyName() == name && parent.Parent.Kind == ast.KindObjectBindingPattern {
			return parent.Parent
		}
	}
	return nil
}

// startsAtNamespace reports whether expression is a path of names that
// starts at a module object, a namespace or an enum: ns, ns.inner, Enum.
func startsAtNamespace(ch *checker.Checker, expression *ast.Node) bool {
	for {
		switch expression.Kind {
		case ast.KindPropertyAccessExpression:
			expression = expression.Expression()
		case ast.KindQualifiedName:
			expression = expression.AsQualifiedName().Left
		case ast.KindIdentifier:
			symbol := resolveSymbol(ch, expression)
			return symbol != nil && symbol.Flags&(ast.SymbolFlagsModule|ast.SymbolFlagsEnum) != 0
		default:
			return false
		}
	}
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

// isPublicMember reports whether member is a public member of a class that
// an entry point exports: code outside the analyzed files may use it.
func isPublicMember(file *lint.File, member *ast.Node) bool {
	return isClassLike(member.Parent) && file.IsPublic(member.Parent) && member.Name() != nil && member.Name().Kind != ast.KindPrivateIdentifier &&
		!hasModifier(member, ast.KindPrivateKeyword) && !hasModifier(member, ast.KindProtectedKeyword)
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
