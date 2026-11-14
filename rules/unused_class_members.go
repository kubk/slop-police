package rules

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/microsoft/typescript-go/internal/api/encoder"
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// unused-class-members reports class methods and accessors (with a body) and
// class fields that are never referenced outside their own bodies or
// initializers, so recursion isn't a use. Fields with decorators, which a
// framework may read, and declare and abstract fields aren't checked. A
// member counts as used when:
//   - some identifier or string literal with its name resolves to it (or to
//     a member it implements or overrides),
//   - it implements or overrides a member declared outside the analyzed
//     files, like a library's base class method that the library calls,
//   - it is named in Pick<Class, "name">,
//   - an instance is passed, returned, stored or asserted where a type (other
//     than its class) with a property of its name is expected,
//   - or a name refers to it the way find-all-references relates names: a
//     property of an object literal or a JSX attribute that has to fit a
//     type with the member, the variable of const { name } = instance, a
//     member of a union, a member that it overrides or implements.
//
// It is optional: it is most of a run's time on a big repository, where it
// resolves every name that a class member has.
func init() {
	lint.Register(&lint.Rule{
		Name:           "unused-class-members",
		DefaultOptions: lint.Options{"includePrivate": false, "includeProtected": false},
		Optional:       true,
		Check:          checkUnusedClassMembers,
	})
}

// memberKey identifies a class member by its class's position, its name and
// whether it's static.
type memberKey struct {
	path       tspath.Path
	classStart int
	name       string
	static     bool
}

type memberCandidate struct {
	file *lint.File
	node *ast.Node
	key  memberKey
}

// usage is the set of used candidate keys, safe for concurrent use. Keys of
// non-candidates are dropped: only candidates are ever looked up.
type usage struct {
	index   map[memberKey]int
	used    []atomic.Bool
	byClass map[memberKey][]int // keyed by class position (empty name)
}

func newUsage(candidates []*memberCandidate) *usage {
	u := &usage{index: map[memberKey]int{}, byClass: map[memberKey][]int{}}
	for _, c := range candidates {
		if _, ok := u.index[c.key]; !ok {
			u.index[c.key] = len(u.index)
			class := memberKey{path: c.key.path, classStart: c.key.classStart}
			u.byClass[class] = append(u.byClass[class], u.index[c.key])
		}
	}
	u.used = make([]atomic.Bool, len(u.index))
	return u
}

func (u *usage) add(key memberKey) {
	if i, ok := u.index[key]; ok {
		u.used[i].Store(true)
	}
}

func (u *usage) has(key memberKey) bool {
	i, ok := u.index[key]
	return ok && u.used[i].Load()
}

func (u *usage) classHasUnused(class *ast.Node) bool {
	for _, i := range u.byClass[classMemberKey(class, "")] {
		if !u.used[i].Load() {
			return true
		}
	}
	return false
}

func checkUnusedClassMembers(ctx *lint.Context, options lint.Options) []lint.Finding {
	phase := ctx.Timing.Phase("unused-class-members")
	candidates := collectMemberCandidates(ctx, options)
	if len(candidates) == 0 {
		return nil
	}

	// Declarations whose references mark candidates used: the candidates
	// themselves, plus the interface and base class members they implement
	// or override.
	membersByDeclaration := map[declKey][]memberKey{}
	names := map[string]bool{}
	for _, c := range candidates {
		membersByDeclaration[keyOfDeclaration(c.node)] = []memberKey{c.key}
		names[c.key.name] = true
	}
	external := addInheritedMembers(ctx, candidates, membersByDeclaration)
	phase("candidates and interfaces")

	used := newUsage(candidates)
	for _, key := range external {
		used.add(key)
	}
	ctx.ForEachFile(ctx.Files, func(file *lint.File, ch *checker.Checker) {
		collectReferencedMembers(file, ch, names, membersByDeclaration, used)
	})
	phase("referenced members")

	structuralFiles := filesWithUnusedCandidateClasses(ctx, candidates, used)
	classNames := map[string]bool{}
	for _, c := range candidates {
		if name := c.node.Parent.Name(); name != nil && !used.has(c.key) {
			classNames[name.Text()] = true
		}
	}
	ctx.ForEachFile(structuralFiles, func(file *lint.File, ch *checker.Checker) {
		collectStructurallyUsedMembers(file, ch, used, classNames)
	})
	phase(fmt.Sprintf("structural uses (%d files)", len(structuralFiles)))

	var remaining []*memberCandidate
	for _, c := range candidates {
		if !used.has(c.key) {
			remaining = append(remaining, c)
		}
	}
	exported := collectRelatedReferences(ctx, remaining, used)
	phase(fmt.Sprintf("related references (%d)", len(remaining)))
	referenced := hasReferencesOutsideDeclaration(ctx, exported)
	phase(fmt.Sprintf("find references (%d)", len(exported)))
	for i, c := range exported {
		if referenced[i] {
			used.add(c.key)
		}
	}

	var findings []lint.Finding
	for _, c := range remaining {
		if used.has(c.key) {
			continue
		}
		className := "<anonymous>"
		if name := c.node.Parent.Name(); name != nil {
			className = name.Text()
		}
		message := fmt.Sprintf("%s %s.%s is never used", memberKind(c.node), className, c.key.name)
		findings = append(findings, lint.Finding{File: c.file, Node: c.node.Name(), Message: message})
	}
	return findings
}

func collectMemberCandidates(ctx *lint.Context, options lint.Options) []*memberCandidate {
	var candidates []*memberCandidate
	for _, file := range ctx.Files {
		if file.IsTest {
			continue
		}
		forEachNode(file.Source.AsNode(), func(node *ast.Node) {
			if isCheckedClassMember(node) && shouldCheckVisibility(node, options) && node.Name().Kind == ast.KindIdentifier && !isProtocolMember(node) && !isPublicMember(file, node) {
				key := classMemberKey(node.Parent, node.Name().Text())
				key.static = hasModifier(node, ast.KindStaticKeyword)
				candidates = append(candidates, &memberCandidate{file, node, key})
			}
		})
	}
	return candidates
}

// conversionMethods are called by the language itself: JSON.stringify calls
// toJSON, string conversion toString, number conversion valueOf, and await
// calls then.
var conversionMethods = map[string]bool{"toJSON": true, "toString": true, "valueOf": true, "then": true}

// isProtocolMember reports whether the runtime calls the instance member
// node: a conversion method, or the iterator methods of a class that is its
// own iterator ([Symbol.iterator]() { return this }), which for...of calls.
func isProtocolMember(node *ast.Node) bool {
	if hasModifier(node, ast.KindStaticKeyword) {
		return false
	}
	switch name := node.Name().Text(); {
	case conversionMethods[name]:
		return true
	case name == "next" || name == "return" || name == "throw":
		for _, member := range node.Parent.Members() {
			if symbolName := wellKnownSymbolName(member.Name()); symbolName == "iterator" || symbolName == "asyncIterator" {
				return true
			}
		}
	}
	return false
}

// wellKnownSymbolName returns "iterator" for [Symbol.iterator], and so on,
// or "" for other names.
func wellKnownSymbolName(name *ast.Node) string {
	if name == nil || name.Kind != ast.KindComputedPropertyName {
		return ""
	}
	expression := name.AsComputedPropertyName().Expression
	if expression.Kind != ast.KindPropertyAccessExpression || !ast.IsIdentifier(expression.Expression()) || expression.Expression().Text() != "Symbol" {
		return ""
	}
	return expression.AsPropertyAccessExpression().Name().Text()
}

// customElementCallbacks are the members of a custom element (a class
// extending HTMLElement) that the browser calls, by whether they're static.
var customElementCallbacks = map[bool]map[string]bool{
	false: {
		"connectedCallback": true, "disconnectedCallback": true, "adoptedCallback": true, "attributeChangedCallback": true,
		"connectedMoveCallback": true, "formAssociatedCallback": true, "formDisabledCallback": true, "formResetCallback": true,
		"formStateRestoreCallback": true,
	},
	true: {"observedAttributes": true, "formAssociated": true},
}

// rpcBaseClasses are the Cloudflare Workers classes whose subclasses'
// public methods other Workers call over RPC.
var rpcBaseClasses = map[string]bool{"WorkerEntrypoint": true, "DurableObject": true, "RpcTarget": true, "WorkflowEntrypoint": true}

// calledByRuntime reports, for a class extending library classes, which of
// its members the runtime calls: a custom element's lifecycle callbacks, and
// every public instance member of a Workers RPC class.
func calledByRuntime(ctx *lint.Context, ch *checker.Checker, class *ast.Node) func(key memberKey) bool {
	var customElement, rpc bool
	visited := map[*ast.Symbol]bool{}
	var visit func(symbol *ast.Symbol)
	visit = func(symbol *ast.Symbol) {
		if symbol == nil || visited[symbol] {
			return
		}
		visited[symbol] = true
		external := len(symbol.Declarations) > 0 && !ctx.IsAnalyzedPath(pathOf(symbol.Declarations[0]))
		customElement = customElement || external && symbol.Name == "HTMLElement"
		rpc = rpc || external && rpcBaseClasses[symbol.Name]
		// Base types of generic classes are references: go through the
		// declared type of their symbol.
		for _, base := range ch.GetBaseTypes(ch.GetDeclaredTypeOfSymbol(symbol)) {
			visit(base.Symbol())
		}
	}
	visit(class.Symbol())
	return func(key memberKey) bool {
		return customElement && customElementCallbacks[key.static][key.name] || rpc && !key.static
	}
}

// addInheritedMembers maps the members a class implements or overrides (as
// the class file's checker sees them) to the candidates with their names:
// members of implemented interfaces and of the extended class, whose static
// side has the static ones. It returns the candidates that implement or
// override a member declared outside the analyzed files, such as a library's
// interface or base class: the library calls them, and its code isn't
// searched for references.
func addInheritedMembers(ctx *lint.Context, candidates []*memberCandidate, membersByDeclaration map[declKey][]memberKey) []memberKey {
	byFile := map[*lint.File]map[*ast.Node][]*memberCandidate{} // by class
	var files []*lint.File
	for _, c := range candidates {
		class := c.node.Parent
		if len(nodeList(class.ClassLikeData().HeritageClauses)) == 0 {
			continue
		}
		if byFile[c.file] == nil {
			byFile[c.file] = map[*ast.Node][]*memberCandidate{}
			files = append(files, c.file)
		}
		byFile[c.file][class] = append(byFile[c.file][class], c)
	}
	var mu sync.Mutex
	var external []memberKey
	constructors := &constructorTypes{ctx: ctx, byClass: map[constructorTypesKey][]*checker.Type{}}
	ctx.ForEachFile(files, func(file *lint.File, ch *checker.Checker) {
		for class, classCandidates := range byFile[file] {
			instance, static := inheritedProperties(ch, class, constructors)
			runtime := calledByRuntime(ctx, ch, class)
			for _, c := range classCandidates {
				properties := instance
				if c.key.static {
					properties = static
				}
				mu.Lock()
				if runtime(c.key) && !hasModifier(c.node, ast.KindPrivateKeyword) && !hasModifier(c.node, ast.KindProtectedKeyword) {
					external = append(external, c.key)
				}
				for _, property := range properties[c.key.name] {
					for _, declaration := range property.Declarations {
						if ctx.IsAnalyzedPath(pathOf(declaration)) {
							key := keyOfDeclaration(declaration)
							membersByDeclaration[key] = append(membersByDeclaration[key], c.key)
						} else {
							external = append(external, c.key)
						}
					}
				}
				mu.Unlock()
			}
		}
	})
	return external
}

// inheritedProperties returns, by name, the instance properties of the
// class's implemented interfaces and extended class, and the static
// properties of its extended class and of the library's constructor types
// for the classes it extends.
func inheritedProperties(ch *checker.Checker, class *ast.Node, constructors *constructorTypes) (instance, static map[string][]*ast.Symbol) {
	instance, static = map[string][]*ast.Symbol{}, map[string][]*ast.Symbol{}
	add := func(properties map[string][]*ast.Symbol, t *checker.Type) {
		if t == nil {
			return
		}
		for _, property := range ch.GetPropertiesOfType(t) {
			properties[property.Name] = append(properties[property.Name], property)
		}
	}
	for _, clause := range nodeList(class.ClassLikeData().HeritageClauses) {
		for _, typeNode := range nodeList(clause.AsHeritageClause().Types) {
			// In an extends clause, the type of the node is the base class's
			// instance type, and the type of its expression the constructor's.
			add(instance, ch.GetTypeAtLocation(typeNode))
			if clause.AsHeritageClause().Token == ast.KindExtendsKeyword {
				add(static, ch.GetTypeAtLocation(typeNode.AsExpressionWithTypeArguments().Expression))
				for _, t := range constructors.ofAncestors(ch, ch.GetTypeAtLocation(typeNode)) {
					add(static, t)
				}
			}
		}
	}
	return instance, static
}

// constructorTypes finds, for classes declared outside the analyzed files,
// the types their library declares for subclass constructors: interfaces and
// type aliases in the class's file with a construct signature returning the
// class, like React's ComponentClass for Component. Their properties are the
// static members the library reads from subclasses, such as
// getDerivedStateFromProps. It's safe for concurrent use.
type constructorTypes struct {
	ctx     *lint.Context
	mu      sync.Mutex
	byClass map[constructorTypesKey][]*checker.Type
}

// constructorTypesKey is per checker: types belong to the checker that
// created them.
type constructorTypesKey struct {
	ch    *checker.Checker
	class *ast.Symbol
}

// ofAncestors returns the constructor types of base, a class instance type,
// and of the classes it extends.
func (c *constructorTypes) ofAncestors(ch *checker.Checker, base *checker.Type) []*checker.Type {
	var types []*checker.Type
	visited := map[*ast.Symbol]bool{}
	var visit func(t *checker.Type)
	visit = func(t *checker.Type) {
		symbol := t.Symbol()
		if symbol == nil || symbol.Flags&ast.SymbolFlagsClass == 0 || visited[symbol] {
			return
		}
		visited[symbol] = true
		types = append(types, c.of(ch, symbol)...)
		for _, baseType := range ch.GetBaseTypes(ch.GetDeclaredTypeOfSymbol(symbol)) {
			visit(baseType)
		}
	}
	visit(base)
	return types
}

func (c *constructorTypes) of(ch *checker.Checker, class *ast.Symbol) []*checker.Type {
	key := constructorTypesKey{ch, class}
	c.mu.Lock()
	types, ok := c.byClass[key]
	c.mu.Unlock()
	if ok {
		return types
	}
	files := map[*ast.SourceFile]bool{}
	for _, declaration := range class.Declarations {
		if !c.ctx.IsAnalyzedPath(pathOf(declaration)) {
			files[ast.GetSourceFileOfNode(declaration)] = true
		}
	}
	for file := range files {
		forEachNode(file.AsNode(), func(node *ast.Node) {
			if node.Kind != ast.KindInterfaceDeclaration && node.Kind != ast.KindTypeAliasDeclaration {
				return
			}
			t := ch.GetDeclaredTypeOfSymbol(node.Symbol())
			for _, signature := range ch.GetSignaturesOfType(t, checker.SignatureKindConstruct) {
				if ch.GetReturnTypeOfSignature(signature).Symbol() == class {
					types = append(types, t)
					break
				}
			}
		})
	}
	c.mu.Lock()
	c.byClass[key] = types
	c.mu.Unlock()
	return types
}

func implementedTypes(class *ast.Node) []*ast.Node {
	var types []*ast.Node
	for _, clause := range nodeList(class.ClassLikeData().HeritageClauses) {
		if clause.AsHeritageClause().Token == ast.KindImplementsKeyword {
			types = append(types, nodeList(clause.AsHeritageClause().Types)...)
		}
	}
	return types
}

// collectReferencedMembers resolves every identifier and string literal of
// the file named like a candidate, and every Pick<Class, "names">. A name
// whose candidates are all used is resolved too: an alias named like one
// candidate can resolve to another, as in export import bar = A.foo.
func collectReferencedMembers(file *lint.File, ch *checker.Checker, names map[string]bool, membersByDeclaration map[declKey][]memberKey, used *usage) {
	forEachNode(file.Source.AsNode(), func(node *ast.Node) {
		if isPickTypeReference(node) {
			arguments := node.TypeArguments()
			if class := classNodeForTypeNode(ch, arguments[0]); class != nil {
				for _, name := range stringLiteralTypeNames(arguments[1]) {
					used.add(classMemberKey(class, name))
				}
			}
		}
		switch node.Kind {
		case ast.KindIdentifier, ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		default:
			return
		}
		if !names[node.Text()] || isClassMemberDeclarationName(node) {
			return
		}
		symbol := resolveSymbol(ch, node)
		if symbol == nil {
			return
		}
		for _, declaration := range symbol.Declarations {
			switch declaration.Kind {
			case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor, ast.KindPropertyDeclaration, ast.KindMethodSignature, ast.KindPropertySignature:
				for _, key := range membersByDeclaration[keyOfDeclaration(declaration)] {
					if !isInsideMember(node, key) {
						used.add(key)
					}
				}
			}
		}
	})
}

// collectStructurallyUsedMembers marks the members of a class instance that
// a type it has to fit requires, unless that type is the class itself: the
// instance is used through that type, which needn't be an interface the
// class implements. Instances fit a type when they're passed as an argument
// or a JSX attribute, returned, assigned or stored in a typed place, or
// asserted to it with as or satisfies.
//
// Checking an expression for its type is slow, so the instance's class is
// found from its symbols where possible. Only files that name a class with
// unused members, which can narrow a value to it or cast it, also get their
// other expressions checked.
//
// It is a pass of its own, after collectReferencedMembers has seen every
// file: most classes have no unused member left by then, so few files are
// visited and fewer are checked. In the same visit it has to check every
// file that names a class with candidates, which is slower wherever the run
// stays under its memory budget (see lint.MemoryBudget).
func collectStructurallyUsedMembers(file *lint.File, ch *checker.Checker, used *usage, classNames map[string]bool) {
	namesClass := false
	for name := range file.Source.GetNameTable() {
		if classNames[name] {
			namesClass = true
			break
		}
	}
	forEachNode(file.Source.AsNode(), func(node *ast.Node) {
		var expression *ast.Node
		var target *checker.Type
		switch {
		case node.Kind == ast.KindAsExpression || node.Kind == ast.KindSatisfiesExpression:
			// The outermost of x as unknown as T: x has to fit T.
			if node.Parent.Kind == ast.KindAsExpression || node.Parent.Kind == ast.KindSatisfiesExpression || ast.IsConstTypeReference(node.Type()) {
				return
			}
			expression = unwrapExpression(node)
			target = ch.GetTypeFromTypeNode(node.Type())
		case isContextuallyTypedPosition(node):
			expression = node
		default:
			return
		}
		class := instanceClass(ch, expression)
		if class == nil && namesClass && mayBeInstance(expression) && !returnsNoInstance(ch, expression) {
			class = classNodeForType(ch.GetTypeAtLocation(expression))
		}
		// A class whose candidates are all used has nothing left to mark.
		if class == nil || !used.classHasUnused(class) {
			return
		}
		if target == nil {
			target = ch.GetContextualType(expression, checker.ContextFlagsNone)
		}
		if target == nil {
			return
		}
		required := map[string]bool{}
		for _, t := range unionMembers(ch.GetNonNullableType(target)) {
			// The members of the class and of its base classes are linked
			// already.
			if isClassOrAncestor(ch, classNodeForType(t), class) {
				return
			}
			for _, property := range ch.GetPropertiesOfType(ch.GetApparentType(t)) {
				required[property.Name] = true
			}
		}
		for _, member := range class.Members() {
			if isCheckedClassMember(member) && !hasModifier(member, ast.KindStaticKeyword) && member.Name().Kind == ast.KindIdentifier && required[member.Name().Text()] {
				used.add(classMemberKey(class, member.Name().Text()))
			}
		}
	})
}

// instanceClass returns the class of the instance that expression is, found
// without checking the expression (which is slow): a new expression of the
// class, this in it, or a variable, parameter or property whose type is the
// class.
func instanceClass(ch *checker.Checker, expression *ast.Node) *ast.Node {
	if expression == nil {
		return nil
	}
	switch expression.Kind {
	case ast.KindParenthesizedExpression, ast.KindNonNullExpression:
		return instanceClass(ch, expression.Expression())
	case ast.KindNewExpression:
		if symbol := resolveSymbol(ch, expression.Expression()); symbol != nil {
			return findClassLikeDeclaration(symbol.Declarations)
		}
	case ast.KindThisKeyword:
		// In static members, this is the class itself.
		if container := ast.GetThisContainer(expression, false, false); container != nil && isClassLike(container.Parent) && !hasModifier(container, ast.KindStaticKeyword) {
			return container.Parent
		}
	case ast.KindIdentifier, ast.KindPropertyAccessExpression:
		symbol := resolveSymbol(ch, expression)
		if symbol == nil || symbol.Flags&(ast.SymbolFlagsVariable|ast.SymbolFlagsProperty) == 0 {
			return nil
		}
		return classNodeForType(ch.GetNonNullableType(ch.GetTypeOfSymbol(symbol)))
	}
	return nil
}

// mayBeInstance reports whether expression's value can be a class instance:
// it isn't a function expression, whose type is the function's own, a
// literal, or an operator whose result is a primitive or one of its operands
// that can't be an instance. An array literal is an array, and an object
// literal has a type of its own unless all it does is spread: the type of
// { ...this } is this.
func mayBeInstance(expression *ast.Node) bool {
	switch expression.Kind {
	case ast.KindParenthesizedExpression, ast.KindNonNullExpression:
		return mayBeInstance(expression.Expression())
	case ast.KindConditionalExpression:
		conditional := expression.AsConditionalExpression()
		return mayBeInstance(conditional.WhenTrue) || mayBeInstance(conditional.WhenFalse)
	case ast.KindBinaryExpression:
		binary := expression.AsBinaryExpression()
		switch binary.OperatorToken.Kind {
		case ast.KindBarBarToken, ast.KindQuestionQuestionToken, ast.KindBarBarEqualsToken, ast.KindQuestionQuestionEqualsToken:
			return mayBeInstance(binary.Left) || mayBeInstance(binary.Right)
		case ast.KindAmpersandAmpersandToken, ast.KindAmpersandAmpersandEqualsToken, ast.KindEqualsToken, ast.KindCommaToken:
			return mayBeInstance(binary.Right)
		}
		return false
	case ast.KindArrowFunction, ast.KindFunctionExpression, ast.KindStringLiteral, ast.KindNumericLiteral, ast.KindBigIntLiteral,
		ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateExpression, ast.KindRegularExpressionLiteral,
		ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword, ast.KindArrayLiteralExpression,
		ast.KindPrefixUnaryExpression, ast.KindPostfixUnaryExpression, ast.KindTypeOfExpression, ast.KindVoidExpression, ast.KindDeleteExpression:
		return false
	case ast.KindObjectLiteralExpression:
		properties := expression.Properties()
		return len(properties) > 0 && !slices.ContainsFunc(properties, func(property *ast.Node) bool { return property.Kind != ast.KindSpreadAssignment })
	}
	return true
}

// returnsNoInstance reports whether expression is a call that returns no
// class instance whichever signature of the called function applies. Checking
// the call for its type picks the signature and infers its type arguments,
// which checks the arguments.
func returnsNoInstance(ch *checker.Checker, expression *ast.Node) bool {
	if expression.Kind != ast.KindCallExpression {
		return false
	}
	switch expression.Expression().Kind {
	case ast.KindSuperKeyword, ast.KindImportKeyword:
		return false
	}
	signatures := ch.GetSignaturesOfType(ch.GetNonNullableType(ch.GetTypeAtLocation(expression.Expression())), checker.SignatureKindCall)
	for _, signature := range signatures {
		returned := ch.GetReturnTypeOfSignature(signature)
		if classNodeForType(returned) != nil || len(signature.TypeParameters()) > 0 && canBecomeInstance(returned) {
			return false
		}
	}
	return len(signatures) > 0
}

// canBecomeInstance reports whether type arguments can make a class
// instance's type of t, the return type of a generic function: they can of a
// type parameter and of a type worked out from one, and they can reduce a
// union to one of its types, unless a primitive type such as null stays in
// it.
func canBecomeInstance(t *checker.Type) bool {
	switch {
	case t.Flags()&(checker.TypeFlagsInstantiable|checker.TypeFlagsIntersection) != 0:
		return true
	case t.Flags()&checker.TypeFlagsUnion != 0:
		return !slices.ContainsFunc(t.Types(), func(member *checker.Type) bool { return member.Flags()&checker.TypeFlagsPrimitive != 0 })
	}
	return false
}

// isClassOrAncestor reports whether ancestor is class or a class it extends.
func isClassOrAncestor(ch *checker.Checker, ancestor, class *ast.Node) bool {
	if ancestor == nil {
		return false
	}
	visited := map[*ast.Symbol]bool{}
	var visit func(symbol *ast.Symbol) bool
	visit = func(symbol *ast.Symbol) bool {
		if symbol == nil || visited[symbol] {
			return false
		}
		visited[symbol] = true
		if findClassLikeDeclaration(symbol.Declarations) == ancestor {
			return true
		}
		for _, base := range ch.GetBaseTypes(ch.GetDeclaredTypeOfSymbol(symbol)) {
			if visit(base.Symbol()) {
				return true
			}
		}
		return false
	}
	return visit(class.Symbol())
}

// isContextuallyTypedPosition reports whether node is an expression whose
// value goes where a type may be expected of it: an argument, a JSX
// attribute, a return value (also of an arrow function), an initializer of a
// variable, property or object literal property, an array element, or the
// right side of an assignment.
func isContextuallyTypedPosition(node *ast.Node) bool {
	parent := node.Parent
	if parent == nil || !ast.IsExpression(node) {
		return false
	}
	switch parent.Kind {
	case ast.KindCallExpression, ast.KindNewExpression:
		return slices.Contains(parent.Arguments(), node)
	case ast.KindJsxExpression:
		return parent.Parent.Kind == ast.KindJsxAttribute
	case ast.KindReturnStatement, ast.KindArrayLiteralExpression:
		return true
	case ast.KindArrowFunction:
		return parent.Body() == node
	case ast.KindVariableDeclaration, ast.KindPropertyDeclaration, ast.KindPropertyAssignment:
		return parent.Initializer() == node
	case ast.KindBinaryExpression:
		binary := parent.AsBinaryExpression()
		return binary.Right == node && binary.OperatorToken.Kind == ast.KindEqualsToken
	}
	return false
}

func unionMembers(t *checker.Type) []*checker.Type {
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		return t.Types()
	}
	return []*checker.Type{t}
}

// filesWithUnusedCandidateClasses returns the files of projects whose program
// contains a class with an unused candidate. A checker only produces classes
// declared in its own program, so instances can't be used structurally
// elsewhere.
func filesWithUnusedCandidateClasses(ctx *lint.Context, candidates []*memberCandidate, used *usage) []*lint.File {
	relevant := map[*lint.Project]bool{}
	for _, c := range candidates {
		if !used.has(c.key) {
			for _, p := range ctx.ProjectsContaining(c.file.Source) {
				relevant[p] = true
			}
		}
	}
	var files []*lint.File
	for _, f := range ctx.Files {
		if relevant[f.Project] {
			files = append(files, f)
		}
	}
	return files
}

// collectRelatedReferences marks the candidates that find-all-references
// for their names, in the programs with their files, finds a reference to in
// an analyzed file, outside class member names and their own bodies. It
// returns the unmarked candidates that an export refers to, like
// export const { name } = instance: find-all-references goes on to the
// imports of the export, and this doesn't (see hasReferencesOutsideDeclaration).
//
// It resolves the names that candidates have once per file, where one
// find-all-references request per candidate looks up every occurrence of its
// name in every file of every program with the candidate again. A file that
// several programs share with a candidate's file is searched for the
// candidate in one of them (see memberSearchGroup).
func collectRelatedReferences(ctx *lint.Context, candidates []*memberCandidate, used *usage) []*memberCandidate {
	owners := map[tspath.Path]*lint.Project{}
	for _, file := range ctx.Files {
		owners[file.Source.Path()] = file.Project
	}
	searches := map[*lint.Project]*memberSearch{}
	var projects []*lint.Project
	groups := map[string]*memberSearchGroup{}
	scopes := map[memberKey]*ast.Node{}
	for _, c := range candidates {
		var containing []*lint.Project
		id := ""
		for _, p := range ctx.ProjectsContaining(c.file.Source) {
			if slices.Contains(containing, p) {
				continue
			}
			containing = append(containing, p)
			id += p.ConfigPath + "\x00"
			if searches[p] == nil {
				searches[p] = &memberSearch{keys: map[memberKey]bool{}, paths: map[tspath.Path]bool{}, byDeclaration: map[declKey][]memberKey{}}
				projects = append(projects, p)
			}
			searches[p].keys[c.key] = true
			searches[p].paths[c.file.Source.Path()] = true
		}
		group := groups[id]
		if group == nil {
			group = newMemberSearchGroup(containing)
			groups[id] = group
			for _, p := range containing {
				searches[p].groups = append(searches[p].groups, group)
			}
		}
		group.names[c.key.name] = true
		// Find-all-references looks for a private property or method in
		// its class declaration only (getSymbolScope).
		if (c.node.Kind == ast.KindPropertyDeclaration || c.node.Kind == ast.KindMethodDeclaration) && hasModifier(c.node, ast.KindPrivateKeyword) {
			if class := ast.FindAncestorKind(c.node, ast.KindClassDeclaration); class != nil {
				scopes[c.key] = class
			}
		}
	}

	// What a reference has to relate to, to be one to a candidate: what the
	// candidate's own name relates to.
	ctx.ForEachProgramFile(projects, func(p *lint.Project, source *ast.SourceFile) bool {
		return searches[p].paths[source.Path()]
	}, func(p *lint.Project, source *ast.SourceFile, ch *checker.Checker) {
		search := searches[p]
		related := newMemberRelations(ch)
		forEachNode(source.AsNode(), func(node *ast.Node) {
			if !isCheckedClassMember(node) || node.Name().Kind != ast.KindIdentifier {
				return
			}
			key := classMemberKey(node.Parent, node.Name().Text())
			key.static = hasModifier(node, ast.KindStaticKeyword)
			symbol := ch.GetSymbolAtLocation(node.Name())
			if !search.keys[key] || symbol == nil {
				return
			}
			related.forEach(node.Name(), symbol, func(symbol *ast.Symbol) {
				search.mu.Lock()
				defer search.mu.Unlock()
				for _, declaration := range symbol.Declarations {
					declaration := keyOfDeclaration(declaration)
					if !slices.Contains(search.byDeclaration[declaration], key) {
						search.byDeclaration[declaration] = append(search.byDeclaration[declaration], key)
					}
				}
			})
		})
	})

	var mu sync.Mutex
	exported := map[memberKey]bool{}
	ctx.ForEachProgramFile(projects, func(p *lint.Project, source *ast.SourceFile) bool {
		// The library's files don't refer to the analyzed files' classes.
		return !p.Program.IsSourceFileDefaultLibrary(source.Path()) && len(searches[p].groupsFor(p, source.Path(), owners)) > 0
	}, func(p *lint.Project, source *ast.SourceFile, ch *checker.Checker) {
		search := searches[p]
		groups := search.groupsFor(p, source.Path(), owners)
		related := newMemberRelations(ch)
		analyzed := ctx.IsAnalyzedPath(source.Path())
		forEachNodeAndJSDoc(source, func(node *ast.Node) {
			switch node.Kind {
			case ast.KindIdentifier, ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
			default:
				return
			}
			// A reference outside the analyzed files only counts as an
			// export.
			if !hasMemberName(groups, node.Text()) || !isMemberReferenceName(node) || !analyzed && !isExportedName(node) {
				return
			}
			symbol := ch.GetSymbolAtLocation(node)
			if symbol == nil {
				return
			}
			related.forEach(node, symbol, func(relative *ast.Symbol) {
				for _, declaration := range relative.Declarations {
					for _, key := range search.byDeclaration[keyOfDeclaration(declaration)] {
						if used.has(key) {
							continue
						}
						if scope := scopes[key]; scope != nil && (source.Path() != key.path || node.Pos() < scope.Pos() || node.End() > scope.End()) {
							continue
						}
						if analyzed && !isInsideMember(node, key) {
							used.add(key)
						} else if isExportedName(node) {
							mu.Lock()
							exported[key] = true
							mu.Unlock()
						}
					}
				}
			})
		})
	})

	var result []*memberCandidate
	for _, c := range candidates {
		if exported[c.key] && !used.has(c.key) {
			result = append(result, c)
		}
	}
	return result
}

// memberSearch is what collectRelatedReferences looks for in a program.
type memberSearch struct {
	keys   map[memberKey]bool   // the candidates in the program's files
	paths  map[tspath.Path]bool // their files
	groups []*memberSearchGroup // the groups they are in

	// byDeclaration has, for the declarations of the symbols that
	// find-all-references searches for a candidate (its search set), the
	// candidate. Declarations stand for their symbols, which differ per
	// program and, for members that aren't declared as they are used, per
	// checker.
	mu            sync.Mutex
	byDeclaration map[declKey][]memberKey
}

// groupsFor returns the groups of candidates that the file at path is
// searched for in the program of p.
func (s *memberSearch) groupsFor(p *lint.Project, path tspath.Path, owners map[tspath.Path]*lint.Project) []*memberSearchGroup {
	var groups []*memberSearchGroup
	for _, group := range s.groups {
		if group.searchedIn(path, owners[path]) == p {
			groups = append(groups, group)
		}
	}
	return groups
}

// memberSearchGroup is the candidates whose files are in the same programs.
// Each of those programs resolves a file that they share in the same way,
// unless they resolve its imports differently. So a file is searched for the
// group's names in one of them: the program of the project that analyzes the
// file, whose checkers have resolved the file's types in the passes before,
// else the first that has the file.
type memberSearchGroup struct {
	names    map[string]bool
	projects []*lint.Project
	first    map[tspath.Path]*lint.Project // by file, the first of projects with it
}

func newMemberSearchGroup(projects []*lint.Project) *memberSearchGroup {
	group := &memberSearchGroup{names: map[string]bool{}, projects: projects, first: map[tspath.Path]*lint.Project{}}
	for _, p := range projects {
		for _, source := range p.Program.GetSourceFiles() {
			if group.first[source.Path()] == nil {
				group.first[source.Path()] = p
			}
		}
	}
	return group
}

// searchedIn returns the project in whose program the file at path, which
// owner analyzes (nil for a file that isn't analyzed), is searched for the
// group's names, or nil.
func (g *memberSearchGroup) searchedIn(path tspath.Path, owner *lint.Project) *lint.Project {
	if owner != nil && slices.Contains(g.projects, owner) {
		return owner
	}
	return g.first[path]
}

func hasMemberName(groups []*memberSearchGroup, name string) bool {
	for _, group := range groups {
		if group.names[name] {
			return true
		}
	}
	return false
}

// hasReferencesOutsideDeclaration reports, per candidate, whether
// find-all-references in any project containing its file finds a reference
// in an analyzed file that is not a class member's own name. That takes a
// look at every occurrence of the candidate's name in every file of the
// program: see collectRelatedReferences.
func hasReferencesOutsideDeclaration(ctx *lint.Context, candidates []*memberCandidate) []bool {
	var queries []lint.ReferenceQuery
	var owners []int
	for i, c := range candidates {
		sf := c.file.Source
		name := c.node.Name()
		// The JS API passes this UTF-16 offset through unchanged.
		position := c.file.UTF16Offset(lint.Start(name, sf))
		for _, project := range ctx.ProjectsContaining(sf) {
			node := name
			if projectFile := project.Program.GetSourceFileByPath(sf.Path()); projectFile != sf {
				// Another parse of the same file: find the same node by index,
				// as API node handles do.
				node = encoder.GetNodeIndexTable(projectFile).Nodes[encoder.GetNodeIndexTable(sf).GetIndex(name)]
			}
			queries = append(queries, lint.ReferenceQuery{Project: project, Node: node, Position: position})
			owners = append(owners, i)
		}
	}
	answers := ctx.AnyReference(queries, func(query int, ref *ast.Node) bool {
		return ctx.IsAnalyzedPath(pathOf(ref)) && !isClassMemberDeclarationName(ref) && !isInsideMember(ref, candidates[owners[query]].key)
	})
	result := make([]bool, len(candidates))
	for qi, ok := range answers {
		result[owners[qi]] = result[owners[qi]] || ok
	}
	return result
}

// isExportedName reports whether find-all-references may go on to the
// imports of name, a reference to a class member (getImportOrExportSymbol in
// typescript-go's internal/ls/importTracker.go): it's the variable of
// export const { name } = instance, which the member is the property of, or
// it's exported itself, as in export default instance.name. It can report
// more names than that, never fewer.
func isExportedName(name *ast.Node) bool {
	// A JavaScript file exports with assignments and JSDoc tags too.
	if ast.IsInJSFile(name) {
		return true
	}
	parent := name.Parent
	switch parent.Kind {
	case ast.KindBindingElement:
		statement := ast.WalkUpBindingElementsAndPatterns(parent).Parent.Parent
		return parent.Name() == name && statement != nil && statement.Kind == ast.KindVariableStatement && ast.HasSyntacticModifier(statement, ast.ModifierFlagsExport)
	case ast.KindExportAssignment:
		return true
	}
	return parent.Parent != nil && parent.Parent.Kind == ast.KindExportAssignment
}

// isMemberReferenceName reports whether node is where find-all-references
// looks for a reference to a member (isValidReferencePosition in
// typescript-go's internal/ls/findallreferences.go): an identifier, or a
// string that names a property, as in x["name"], { "name": 1 }, T["name"]
// and Object.defineProperty(x, "name", {}). The names of class members,
// imports and exports aren't references to members, and the name of a type
// isn't a reference to a value.
func isMemberReferenceName(node *ast.Node) bool {
	parent := node.Parent
	switch node.Kind {
	case ast.KindIdentifier:
		if parent.Kind == ast.KindTypeReference {
			return false
		}
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		switch parent.Kind {
		case ast.KindPropertyDeclaration, ast.KindPropertySignature, ast.KindPropertyAssignment, ast.KindEnumMember,
			ast.KindMethodDeclaration, ast.KindMethodSignature, ast.KindGetAccessor, ast.KindSetAccessor:
			if parent.Name() != node {
				return false
			}
		case ast.KindElementAccessExpression:
			if parent.AsElementAccessExpression().ArgumentExpression != node {
				return false
			}
		case ast.KindComputedPropertyName:
		case ast.KindLiteralType:
			if parent.Parent.Kind != ast.KindIndexedAccessType {
				return false
			}
		case ast.KindCallExpression:
			if !ast.IsBindableObjectDefinePropertyCall(parent) || parent.Arguments()[1] != node {
				return false
			}
		default:
			return false
		}
	default:
		return false
	}
	return parent.Kind != ast.KindImportSpecifier && parent.Kind != ast.KindExportSpecifier && !isClassMemberDeclarationName(node)
}

// memberRelations finds the symbols that a name refers to the way
// find-all-references does (getRelatedSymbol in typescript-go's
// internal/ls/findallreferences.go), with one checker. It lives as long as
// the visit of one file: its symbols would keep a released checker's memory
// from being freed.
type memberRelations struct {
	ch         *checker.Checker
	bases      map[basePropertyKey][]*ast.Symbol
	contextual map[*ast.Node]*checker.Type // by object literal or JSX attributes
}

type basePropertyKey struct {
	class *ast.Symbol
	name  string
}

func newMemberRelations(ch *checker.Checker) *memberRelations {
	return &memberRelations{ch: ch, bases: map[basePropertyKey][]*ast.Symbol{}, contextual: map[*ast.Node]*checker.Type{}}
}

// contextualType returns the type that literal, an object literal or the
// attributes of a JSX element, has to fit: every name in the literal asks
// for it, and the checker works it out again each time.
func (r *memberRelations) contextualType(literal *ast.Node) *checker.Type {
	contextual, ok := r.contextual[literal]
	if !ok {
		contextual = r.ch.GetContextualType(literal, checker.ContextFlagsNone)
		r.contextual[literal] = contextual
	}
	return contextual
}

// forEach calls fn for the symbols that name, which resolves to symbol,
// refers to:
//   - symbol, or the ones that it is made from: the
//     property of every member of a union, the declared property of an
//     instantiated or mapped type,
//   - for a member of a class or an interface, the members of that name it
//     overrides or implements (see baseProperties),
//   - for a property of an object literal or a JSX attribute, the property
//     of the type that the literal has to fit,
//   - for the variable of const { name } = x, the property of x,
//   - for a constructor parameter that declares a property, the property.
func (r *memberRelations) forEach(name *ast.Node, symbol *ast.Symbol, fn func(symbol *ast.Symbol)) {
	ch := r.ch
	static := isStaticSymbol(symbol)
	fromRoots := func(symbol *ast.Symbol) {
		for _, root := range ch.GetRootSymbols(symbol) {
			fn(root)
			if root.Parent == nil || root.Parent.Flags&(ast.SymbolFlagsClass|ast.SymbolFlagsInterface) == 0 {
				continue
			}
			for _, base := range r.baseProperties(root.Parent, root.Name) {
				// A static member and an instance member can have the
				// same name.
				if isStaticSymbol(base) == static {
					fn(base)
				}
			}
		}
	}
	parent := name.Parent
	if element := objectLiteralElementOfName(name); element != nil {
		if contextual := r.contextualType(element.Parent); contextual != nil {
			for _, property := range ch.GetPropertySymbolsFromContextualType(element, contextual, true /*unionSymbolOk*/) {
				fromRoots(property)
			}
		}
		// The property of ({ name: y } = x).
		if property := ch.GetPropertySymbolOfDestructuringAssignment(name); property != nil {
			fn(property)
		}
	}
	fromRoots(symbol)
	if declaration := symbol.ValueDeclaration; declaration != nil && ast.IsParameterPropertyDeclaration(declaration, declaration.Parent) {
		parameter, property := ch.GetSymbolsOfParameterPropertyDeclaration(declaration, symbol.Name)
		if symbol == parameter {
			fromRoots(property)
		}
		return
	}
	if parent.Kind == ast.KindBindingElement && parent.Parent.Kind == ast.KindObjectBindingPattern && parent.Name().Kind == ast.KindIdentifier && parent.PropertyName() == nil {
		if t := ch.GetTypeAtLocation(parent.Parent); t != nil {
			if property := ch.GetPropertyOfType(t, parent.Name().Text()); property != nil {
				fromRoots(property)
			}
		}
	}
}

// objectLiteralElementOfName returns the property of an object literal or
// the JSX attribute that name names, as in { name: 1 }, { ["name"]: 1 } and
// <a name="" />, or nil.
func objectLiteralElementOfName(name *ast.Node) *ast.Node {
	element := name.Parent
	if name.Kind != ast.KindIdentifier && element.Kind == ast.KindComputedPropertyName {
		element = element.Parent
	} else if element.Name() != name {
		return nil
	}
	if !ast.IsObjectLiteralElement(element) && !ast.IsJsxAttribute(element) && !ast.IsJsxSpreadAttribute(element) {
		return nil
	}
	if element.Parent.Kind != ast.KindObjectLiteralExpression && element.Parent.Kind != ast.KindJsxAttributes {
		return nil
	}
	return element
}

// baseProperties returns the properties called name of the types that class
// (the symbol of a class or an interface) extends and implements, and of the
// types that those do.
func (r *memberRelations) baseProperties(class *ast.Symbol, name string) []*ast.Symbol {
	key := basePropertyKey{class, name}
	if properties, ok := r.bases[key]; ok {
		return properties
	}
	var properties []*ast.Symbol
	visited := map[*ast.Symbol]bool{}
	var visit func(symbol *ast.Symbol)
	visit = func(symbol *ast.Symbol) {
		if symbol.Flags&(ast.SymbolFlagsClass|ast.SymbolFlagsInterface) == 0 || visited[symbol] {
			return
		}
		visited[symbol] = true
		for _, declaration := range symbol.Declarations {
			for _, typeNode := range superTypeNodes(declaration) {
				t := r.ch.GetTypeAtLocation(typeNode)
				if t == nil || t.Symbol() == nil {
					continue
				}
				if property := r.ch.GetPropertyOfType(t, name); property != nil {
					properties = append(properties, r.ch.GetRootSymbols(property)...)
				}
				visit(t.Symbol())
			}
		}
	}
	visit(class)
	r.bases[key] = properties
	return properties
}

// superTypeNodes returns the types in the extends and implements clauses of
// a class, and in the extends clause of an interface.
func superTypeNodes(declaration *ast.Node) []*ast.Node {
	switch {
	case declaration.Kind == ast.KindInterfaceDeclaration:
		return ast.GetHeritageElements(declaration, ast.KindExtendsKeyword)
	case isClassLike(declaration):
		var types []*ast.Node
		if extended := ast.GetClassExtendsHeritageElement(declaration); extended != nil {
			types = append(types, extended)
		}
		return append(types, ast.GetImplementsTypeNodes(declaration)...)
	}
	return nil
}

func isStaticSymbol(symbol *ast.Symbol) bool {
	return symbol.ValueDeclaration != nil && ast.HasStaticModifier(symbol.ValueDeclaration)
}

// isInsideMember reports whether node, in any parse of its file, is inside
// the body of the member with key (either accessor of a pair).
func isInsideMember(node *ast.Node, key memberKey) bool {
	for current := node.Parent; current != nil; current = current.Parent {
		if isCheckedClassMember(current) && current.Name().Kind == ast.KindIdentifier {
			own := classMemberKey(current.Parent, current.Name().Text())
			own.static = hasModifier(current, ast.KindStaticKeyword)
			if own == key {
				return true
			}
		}
	}
	return false
}

func isClassMemberDeclarationName(node *ast.Node) bool {
	member := node.Parent
	if member == nil {
		return false
	}
	switch member.Kind {
	case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor, ast.KindPropertyDeclaration:
		return isClassLike(member.Parent) && member.Name() == node
	}
	return false
}

func isPickTypeReference(node *ast.Node) bool {
	if node.Kind != ast.KindTypeReference {
		return false
	}
	reference := node.AsTypeReferenceNode()
	return reference.TypeName.Kind == ast.KindIdentifier && reference.TypeName.Text() == "Pick" && len(nodeList(reference.TypeArguments)) == 2
}

// classNodeForTypeNode resolves a type node to a class declaration: via the
// symbol of its type name, else via its type.
func classNodeForTypeNode(ch *checker.Checker, typeNode *ast.Node) *ast.Node {
	if typeNode.Kind == ast.KindTypeReference {
		if symbol := resolveSymbol(ch, typeNode.AsTypeReferenceNode().TypeName); symbol != nil {
			if class := findClassLikeDeclaration(symbol.Declarations); class != nil {
				return class
			}
		}
	}
	return classNodeForType(ch.GetTypeAtLocation(typeNode))
}

// classNodeForType returns the first class declaration of the type's symbol
// (or, for a type without a symbol, of its alias symbol).
func classNodeForType(t *checker.Type) *ast.Node {
	switch {
	case t == nil:
		return nil
	case t.Symbol() != nil:
		return findClassLikeDeclaration(t.Symbol().Declarations)
	case t.Alias() != nil && t.Alias().Symbol() != nil:
		return findClassLikeDeclaration(t.Alias().Symbol().Declarations)
	}
	return nil
}

func findClassLikeDeclaration(declarations []*ast.Node) *ast.Node {
	for _, declaration := range declarations {
		if isClassLike(declaration) {
			return declaration
		}
	}
	return nil
}

// stringLiteralTypeNames returns the names in "a" | ("b" | `c`).
func stringLiteralTypeNames(typeNode *ast.Node) []string {
	switch typeNode.Kind {
	case ast.KindLiteralType:
		literal := typeNode.AsLiteralTypeNode().Literal
		if literal.Kind == ast.KindStringLiteral || literal.Kind == ast.KindNoSubstitutionTemplateLiteral {
			return []string{literal.Text()}
		}
	case ast.KindUnionType:
		var names []string
		for _, t := range typeNode.AsUnionTypeNode().Types.Nodes {
			names = append(names, stringLiteralTypeNames(t)...)
		}
		return names
	case ast.KindParenthesizedType:
		return stringLiteralTypeNames(typeNode.AsParenthesizedTypeNode().Type)
	}
	return nil
}

func classMemberKey(class *ast.Node, name string) memberKey {
	sf := ast.GetSourceFileOfNode(class)
	anchor := class
	if class.Name() != nil {
		anchor = class.Name()
	}
	return memberKey{path: sf.Path(), classStart: lint.Start(anchor, sf), name: name}
}

func isCheckedClassMember(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor:
		return isClassLike(node.Parent) && node.Body() != nil
	case ast.KindPropertyDeclaration:
		return isClassLike(node.Parent) && !ast.HasDecorators(node) && !hasModifier(node, ast.KindDeclareKeyword) && !hasModifier(node, ast.KindAbstractKeyword)
	}
	return false
}

func shouldCheckVisibility(node *ast.Node, options lint.Options) bool {
	if hasModifier(node, ast.KindPrivateKeyword) {
		return options.Bool("includePrivate")
	}
	if hasModifier(node, ast.KindProtectedKeyword) {
		return options.Bool("includeProtected")
	}
	return true
}

func memberKind(node *ast.Node) string {
	switch node.Kind {
	case ast.KindGetAccessor:
		return "Getter"
	case ast.KindSetAccessor:
		return "Setter"
	case ast.KindPropertyDeclaration:
		return "Property"
	}
	return "Method"
}
