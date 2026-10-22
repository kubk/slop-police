package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

func init() {
	lint.Register(&lint.Rule{
		Name:      "no-redundant-value-alias",
		CheckFile: checkNoRedundantValueAlias,
	})
}

// bindingKey mirrors toBinding's key: the value declaration (or first
// declaration) of the symbol, or the symbol itself when it has none.
type bindingKey struct {
	declaration *ast.Node
	symbol      *ast.Symbol
}

type binding struct {
	key    bindingKey
	symbol *ast.Symbol
}

func toBinding(symbol *ast.Symbol) *binding {
	if symbol == nil || symbol.Flags&(ast.SymbolFlagsValue|ast.SymbolFlagsAlias|ast.SymbolFlagsExportValue) == 0 {
		return nil
	}
	declaration := symbol.ValueDeclaration
	if declaration == nil && len(symbol.Declarations) > 0 {
		declaration = symbol.Declarations[0]
	}
	if declaration != nil {
		return &binding{key: bindingKey{declaration: declaration}, symbol: symbol}
	}
	return &binding{key: bindingKey{symbol: symbol}, symbol: symbol}
}

type aliasAnalysis struct {
	candidates []*ast.Node
	references []*ast.Node
	writes     []*ast.Node
	bindings   map[*ast.Node]*binding
}

func checkNoRedundantValueAlias(file *lint.File, ch *checker.Checker, _ lint.Options, report lint.Report) {
	analysis := collectAliasFile(file.Source)
	if len(analysis.candidates) == 0 {
		return
	}
	analysis.bindings = make(map[*ast.Node]*binding, len(analysis.references))
	for _, node := range analysis.references {
		if node.Parent.Kind == ast.KindShorthandPropertyAssignment {
			continue
		}
		analysis.bindings[node] = toBinding(lint.ResolveSymbol(ch, node, false))
	}
	for _, node := range analysis.references {
		if node.Parent.Kind != ast.KindShorthandPropertyAssignment {
			continue
		}
		analysis.bindings[node] = toBinding(ch.GetShorthandAssignmentValueSymbol(node.Parent))
	}
	for _, node := range findRedundantAliases(ch, analysis) {
		name := node.Name()
		source := unwrapParentheses(node.Initializer())
		report(name, "Local alias "+name.Text()+" only renames "+source.Text()+"; use the original binding directly")
	}
}

func collectAliasFile(sf *ast.SourceFile) *aliasAnalysis {
	analysis := &aliasAnalysis{}
	Visit(sf.AsNode(), func(node *ast.Node) {
		if isAliasDeclaration(node) {
			analysis.candidates = append(analysis.candidates, node)
		}
	})
	if len(analysis.candidates) == 0 {
		return analysis
	}
	names := map[string]bool{}
	for _, node := range analysis.candidates {
		names[node.Name().Text()] = true
		names[unwrapParentheses(node.Initializer()).Text()] = true
	}
	Visit(sf.AsNode(), func(node *ast.Node) {
		if node.Kind == ast.KindIdentifier && names[node.Text()] && isBindingReference(node) {
			analysis.references = append(analysis.references, node)
		}
		for _, target := range assignmentTargets(node) {
			visitAssignmentTarget(target, func(identifier *ast.Node) {
				if names[identifier.Text()] && isBindingReference(identifier) {
					analysis.writes = append(analysis.writes, identifier)
				}
			})
		}
	})
	return analysis
}

func isAliasDeclaration(node *ast.Node) bool {
	if node.Kind != ast.KindVariableDeclaration {
		return false
	}
	decl := node.AsVariableDeclaration()
	if node.Name().Kind != ast.KindIdentifier || decl.Initializer == nil || decl.Type != nil {
		return false
	}
	statement := node.Parent.Parent
	return unwrapParentheses(decl.Initializer).Kind == ast.KindIdentifier &&
		statement.Kind == ast.KindVariableStatement &&
		!hasModifier(statement, ast.KindExportKeyword) &&
		statement.Parent.Kind != ast.KindSourceFile
}

func isBindingReference(node *ast.Node) bool {
	parent := node.Parent
	if parent.Kind == ast.KindPropertyAccessExpression && parent.Name() == node {
		return false
	}
	if parent.Name() == node && parent.Kind != ast.KindVariableDeclaration && parent.Kind != ast.KindShorthandPropertyAssignment {
		return false
	}
	return !(parent.Kind == ast.KindBindingElement && parent.AsBindingElement().PropertyName == node)
}

func assignmentTargets(node *ast.Node) []*ast.Node {
	switch node.Kind {
	case ast.KindBinaryExpression:
		binary := node.AsBinaryExpression()
		if kind := binary.OperatorToken.Kind; kind >= ast.KindFirstAssignment && kind <= ast.KindLastAssignment {
			return []*ast.Node{binary.Left}
		}
	case ast.KindPrefixUnaryExpression:
		unary := node.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return []*ast.Node{unary.Operand}
		}
	case ast.KindPostfixUnaryExpression:
		unary := node.AsPostfixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return []*ast.Node{unary.Operand}
		}
	case ast.KindForOfStatement, ast.KindForInStatement:
		initializer := node.AsForInOrOfStatement().Initializer
		if initializer.Kind != ast.KindVariableDeclarationList {
			return []*ast.Node{initializer}
		}
	}
	return nil
}

func visitAssignmentTarget(node *ast.Node, visitIdentifier func(*ast.Node)) {
	node = unwrapParentheses(node)
	switch node.Kind {
	case ast.KindIdentifier:
		visitIdentifier(node)
	case ast.KindArrayLiteralExpression:
		for _, element := range node.AsArrayLiteralExpression().Elements.Nodes {
			visitAssignmentTarget(element, visitIdentifier)
		}
	case ast.KindObjectLiteralExpression:
		for _, property := range node.AsObjectLiteralExpression().Properties.Nodes {
			switch property.Kind {
			case ast.KindShorthandPropertyAssignment:
				visitIdentifier(property.Name())
			case ast.KindPropertyAssignment:
				visitAssignmentTarget(property.AsPropertyAssignment().Initializer, visitIdentifier)
			case ast.KindSpreadAssignment:
				visitAssignmentTarget(property.AsSpreadAssignment().Expression, visitIdentifier)
			}
		}
	case ast.KindSpreadElement:
		visitAssignmentTarget(node.AsSpreadElement().Expression, visitIdentifier)
	case ast.KindBinaryExpression:
		binary := node.AsBinaryExpression()
		if binary.OperatorToken.Kind == ast.KindEqualsToken {
			visitAssignmentTarget(binary.Left, visitIdentifier)
		}
	}
}

func findRedundantAliases(ch *checker.Checker, analysis *aliasAnalysis) []*ast.Node {
	keyOf := func(node *ast.Node) (bindingKey, bool) {
		b := analysis.bindings[node]
		if b == nil {
			return bindingKey{}, false
		}
		return b.key, true
	}
	writtenKeys := map[bindingKey]bool{}
	for _, write := range analysis.writes {
		if key, ok := keyOf(write); ok {
			writtenKeys[key] = true
		}
	}
	referencesByKey := map[bindingKey][]*ast.Node{}
	for _, reference := range analysis.references {
		if key, ok := keyOf(reference); ok {
			referencesByKey[key] = append(referencesByKey[key], reference)
		}
	}

	var result []*ast.Node
	for _, node := range analysis.candidates {
		initializer := unwrapParentheses(node.Initializer())
		source := analysis.bindings[initializer]
		aliasKey, ok := keyOf(node.Name())
		if source == nil || !ok || !isConstVariable(source.symbol) {
			continue
		}
		if writtenKeys[aliasKey] || writtenKeys[source.key] {
			continue
		}
		var aliasReferences []*ast.Node
		for _, reference := range referencesByKey[aliasKey] {
			if reference != node.Name() {
				aliasReferences = append(aliasReferences, reference)
			}
		}
		sourceName := initializer.Text()
		visibleEverywhere := true
		for _, reference := range aliasReferences {
			b := toBinding(ch.ResolveName(sourceName, reference, ast.SymbolFlagsValue, false))
			if b == nil || b.key != source.key {
				visibleEverywhere = false
				break
			}
		}
		if !visibleEverywhere {
			continue
		}
		if hasGuard(node, source.key, keyOf) {
			needsNarrowing := false
			for _, reference := range aliasReferences {
				if needsCapturedNarrowing(reference, node) {
					needsNarrowing = true
					break
				}
			}
			if needsNarrowing {
				continue
			}
		}
		result = append(result, node)
	}
	return result
}

func isConstVariable(symbol *ast.Symbol) bool {
	declaration := symbol.ValueDeclaration
	if declaration == nil || declaration.Kind != ast.KindVariableDeclaration {
		return false
	}
	return declaration.Name().Kind == ast.KindIdentifier &&
		declaration.Parent.Kind == ast.KindVariableDeclarationList &&
		declaration.Parent.Flags&ast.NodeFlagsConst != 0
}

type keyFunc func(*ast.Node) (bindingKey, bool)

func hasGuard(declaration *ast.Node, sourceKey bindingKey, keyOf keyFunc) bool {
	for node := declaration; node.Parent != nil && !isFunctionLikeDeclaration(node.Parent); node = node.Parent {
		parent := node.Parent
		if parent.Kind == ast.KindIfStatement && isGuard(parent.AsIfStatement().Expression, sourceKey, keyOf) {
			return true
		}
		if parent.Kind == ast.KindBlock {
			for _, statement := range parent.AsBlock().Statements.Nodes {
				if statement == node {
					break
				}
				if statement.Kind == ast.KindIfStatement {
					ifStatement := statement.AsIfStatement()
					if ifStatement.ElseStatement == nil && exits(ifStatement.ThenStatement) && isGuard(ifStatement.Expression, sourceKey, keyOf) {
						return true
					}
				}
			}
		}
	}
	return false
}

func isGuard(expression *ast.Node, sourceKey bindingKey, keyOf keyFunc) bool {
	expression = unwrapParentheses(expression)
	if expression.Kind == ast.KindPrefixUnaryExpression && expression.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
		return isGuard(expression.AsPrefixUnaryExpression().Operand, sourceKey, keyOf)
	}
	if expression.Kind == ast.KindBinaryExpression {
		binary := expression.AsBinaryExpression()
		switch binary.OperatorToken.Kind {
		case ast.KindAmpersandAmpersandToken, ast.KindBarBarToken:
			return isGuard(binary.Left, sourceKey, keyOf) || isGuard(binary.Right, sourceKey, keyOf)
		case ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsToken,
			ast.KindExclamationEqualsEqualsToken, ast.KindInstanceOfKeyword:
			return sourceInGuard(binary.Left, sourceKey, keyOf) || sourceInGuard(binary.Right, sourceKey, keyOf)
		}
		return false
	}
	return sourceInGuard(expression, sourceKey, keyOf)
}

func sourceInGuard(expression *ast.Node, sourceKey bindingKey, keyOf keyFunc) bool {
	expression = unwrapParentheses(expression)
	switch expression.Kind {
	case ast.KindIdentifier:
		key, ok := keyOf(expression)
		return ok && key == sourceKey
	case ast.KindPropertyAccessExpression:
		return sourceInGuard(expression.AsPropertyAccessExpression().Expression, sourceKey, keyOf)
	case ast.KindElementAccessExpression:
		return sourceInGuard(expression.AsElementAccessExpression().Expression, sourceKey, keyOf)
	case ast.KindTypeOfExpression:
		return sourceInGuard(expression.AsTypeOfExpression().Expression, sourceKey, keyOf)
	}
	return false
}

func exits(statement *ast.Node) bool {
	if statement.Kind == ast.KindBlock {
		statements := statement.AsBlock().Statements.Nodes
		return len(statements) > 0 && exits(statements[len(statements)-1])
	}
	return statement.Kind == ast.KindReturnStatement || statement.Kind == ast.KindThrowStatement
}

func needsCapturedNarrowing(reference *ast.Node, declaration *ast.Node) bool {
	parent := reference.Parent
	switch parent.Kind {
	case ast.KindPropertyAccessExpression:
		access := parent.AsPropertyAccessExpression()
		if access.Expression == reference && access.QuestionDotToken != nil {
			return false
		}
	case ast.KindElementAccessExpression:
		access := parent.AsElementAccessExpression()
		if access.Expression == reference && access.QuestionDotToken != nil {
			return false
		}
	}
	scope := declaration.Parent
	for scope != nil && !isFunctionLikeDeclaration(scope) && scope.Kind != ast.KindSourceFile {
		scope = scope.Parent
	}
	for node := reference.Parent; node != nil && node != scope; node = node.Parent {
		if node.Kind == ast.KindFunctionDeclaration {
			return true
		}
	}
	return false
}
