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
//   - or find-all-references finds a reference outside class member names.
//
// It is optional: it takes most of a run's time and memory on a big
// repository, where it resolves every reference to every class member.
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
	referenced := hasReferencesOutsideDeclaration(ctx, remaining)
	phase(fmt.Sprintf("find references (%d)", len(remaining)))

	var findings []lint.Finding
	for i, c := range remaining {
		if referenced[i] {
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
// the file named like a candidate, and every Pick<Class, "names">.
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
		if class == nil && namesClass && mayBeInstance(expression) {
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
// it isn't a function expression, whose type is the function's own, or a
// literal.
func mayBeInstance(expression *ast.Node) bool {
	switch expression.Kind {
	case ast.KindArrowFunction, ast.KindFunctionExpression, ast.KindStringLiteral, ast.KindNumericLiteral, ast.KindBigIntLiteral,
		ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateExpression, ast.KindRegularExpressionLiteral,
		ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword:
		return false
	}
	return true
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

// hasReferencesOutsideDeclaration reports, per candidate, whether
// find-all-references in any project containing its file finds a reference
// in an analyzed file that is not a class member's own name.
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
