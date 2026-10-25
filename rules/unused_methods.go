package rules

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/microsoft/typescript-go/internal/api/encoder"
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// unused-methods reports class methods and accessors (with a body) that are
// never referenced. A member counts as used when:
//   - some identifier or string literal with its name resolves to it (or to
//     a member of an interface the class implements),
//   - it is named in Pick<Class, "name">,
//   - an instance is passed to a JSX attribute whose (structural) contextual
//     type has a property with its name,
//   - or find-all-references finds a reference outside class member names.
func init() {
	lint.Register(&lint.Rule{
		Name:           "unused-methods",
		DefaultOptions: lint.Options{"includePrivate": false, "includeProtected": false},
		Check:          checkUnusedMethods,
	})
}

// memberKey identifies a class member by its class's position and its name.
type memberKey struct {
	path       tspath.Path
	classStart int
	name       string
}

type methodCandidate struct {
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

func newUsage(candidates []*methodCandidate) *usage {
	u := &usage{index: map[memberKey]int{}, byClass: map[memberKey][]int{}}
	for _, c := range candidates {
		if _, ok := u.index[c.key]; !ok {
			u.index[c.key] = len(u.index)
			class := memberKey{c.key.path, c.key.classStart, ""}
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

func checkUnusedMethods(ctx *lint.Context, options lint.Options) []lint.Finding {
	phase := ctx.Timing.Phase("unused-methods")
	candidates := collectMethodCandidates(ctx, options)
	if len(candidates) == 0 {
		return nil
	}

	// Declarations whose references mark candidates used: the candidates
	// themselves, plus members of the interfaces their classes implement.
	membersByDeclaration := map[declKey][]memberKey{}
	names := map[string]bool{}
	for _, c := range candidates {
		membersByDeclaration[keyOfDeclaration(c.node)] = []memberKey{c.key}
		names[c.key.name] = true
	}
	external := addImplementedInterfaceMembers(ctx, candidates, membersByDeclaration)
	phase("candidates and interfaces")

	used := newUsage(candidates)
	for _, key := range external {
		used.add(key)
	}
	ctx.ForEachFile(ctx.Files, func(file *lint.File, ch *checker.Checker) {
		collectReferencedMembers(file, ch, names, membersByDeclaration, used)
	})
	phase("referenced members")

	jsxFiles := filesWithUnusedCandidateClasses(ctx, candidates, used)
	ctx.ForEachFile(jsxFiles, func(file *lint.File, ch *checker.Checker) {
		collectContextuallyUsedJsxMembers(file, ch, used)
	})
	phase(fmt.Sprintf("jsx members (%d files)", len(jsxFiles)))

	var remaining []*methodCandidate
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

func collectMethodCandidates(ctx *lint.Context, options lint.Options) []*methodCandidate {
	var candidates []*methodCandidate
	for _, file := range ctx.Files {
		if file.IsTest {
			continue
		}
		forEachNode(file.Source.AsNode(), func(node *ast.Node) {
			if isCheckedClassMember(node) && shouldCheckVisibility(node, options) && node.Name().Kind == ast.KindIdentifier {
				candidates = append(candidates, &methodCandidate{file, node, classMemberKey(node.Parent, node.Name().Text())})
			}
		})
	}
	return candidates
}

// addImplementedInterfaceMembers maps members of implemented interfaces
// (as the class file's checker sees them) to the candidates with their names.
// It returns the candidates that implement a member declared outside the
// analyzed files, such as a library's interface: the library calls them, and
// its code isn't searched for references.
func addImplementedInterfaceMembers(ctx *lint.Context, candidates []*methodCandidate, membersByDeclaration map[declKey][]memberKey) []memberKey {
	byFile := map[*lint.File]map[*ast.Node][]*methodCandidate{} // by class
	var files []*lint.File
	for _, c := range candidates {
		class := c.node.Parent
		if len(implementedTypes(class)) == 0 {
			continue
		}
		if byFile[c.file] == nil {
			byFile[c.file] = map[*ast.Node][]*methodCandidate{}
			files = append(files, c.file)
		}
		byFile[c.file][class] = append(byFile[c.file][class], c)
	}
	var mu sync.Mutex
	var external []memberKey
	ctx.ForEachFile(files, func(file *lint.File, ch *checker.Checker) {
		for class, classCandidates := range byFile[file] {
			for _, typeNode := range implementedTypes(class) {
				properties := map[string]*ast.Symbol{}
				for _, property := range ch.GetPropertiesOfType(ch.GetTypeAtLocation(typeNode)) {
					properties[property.Name] = property
				}
				for _, c := range classCandidates {
					property := properties[c.key.name]
					if property == nil {
						continue
					}
					mu.Lock()
					for _, declaration := range property.Declarations {
						if ctx.IsAnalyzedPath(pathOf(declaration)) {
							key := keyOfDeclaration(declaration)
							membersByDeclaration[key] = append(membersByDeclaration[key], c.key)
						} else {
							external = append(external, c.key)
						}
					}
					mu.Unlock()
				}
			}
		}
	})
	return external
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
			case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor, ast.KindMethodSignature, ast.KindPropertySignature:
				for _, key := range membersByDeclaration[keyOfDeclaration(declaration)] {
					used.add(key)
				}
			}
		}
	})
}

// collectContextuallyUsedJsxMembers marks the members of a class instance
// passed to a JSX attribute that the attribute's contextual type requires,
// unless the contextual type is that class itself.
func collectContextuallyUsedJsxMembers(file *lint.File, ch *checker.Checker, used *usage) {
	forEachNode(file.Source.AsNode(), func(node *ast.Node) {
		if node.Kind != ast.KindJsxExpression || node.Parent.Kind != ast.KindJsxAttribute {
			return
		}
		expression := node.AsJsxExpression().Expression
		// The type of a function expression is the anonymous type of the
		// function's own symbol (or anyFunctionType), never a class.
		if expression == nil || expression.Kind == ast.KindArrowFunction || expression.Kind == ast.KindFunctionExpression {
			return
		}
		class := classNodeForType(ch.GetTypeAtLocation(expression))
		// A class whose candidates are all used has nothing left to mark.
		if class == nil || !used.classHasUnused(class) {
			return
		}
		contextualType := ch.GetContextualType(expression, checker.ContextFlagsNone)
		if contextualType == nil || classNodeForType(contextualType) == class {
			return
		}
		required := map[string]bool{}
		for _, property := range ch.GetPropertiesOfType(ch.GetApparentType(contextualType)) {
			required[property.Name] = true
		}
		for _, member := range class.Members() {
			if isCheckedClassMember(member) && member.Name().Kind == ast.KindIdentifier && required[member.Name().Text()] {
				used.add(classMemberKey(class, member.Name().Text()))
			}
		}
	})
}

// filesWithUnusedCandidateClasses returns the files of projects whose program
// contains a class with an unused candidate. A checker only produces classes
// declared in its own program, so JSX attributes elsewhere cannot mark one.
func filesWithUnusedCandidateClasses(ctx *lint.Context, candidates []*methodCandidate, used *usage) []*lint.File {
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
func hasReferencesOutsideDeclaration(ctx *lint.Context, candidates []*methodCandidate) []bool {
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
	answers := ctx.AnyReference(queries, func(ref *ast.Node) bool {
		return ctx.IsAnalyzedPath(pathOf(ref)) && !isClassMemberDeclarationName(ref)
	})
	result := make([]bool, len(candidates))
	for qi, ok := range answers {
		result[owners[qi]] = result[owners[qi]] || ok
	}
	return result
}

func isClassMemberDeclarationName(node *ast.Node) bool {
	member := node.Parent
	if member == nil {
		return false
	}
	switch member.Kind {
	case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor:
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
	return memberKey{sf.Path(), lint.Start(anchor, sf), name}
}

func isCheckedClassMember(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor:
		return isClassLike(node.Parent) && node.Body() != nil
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
	}
	return "Method"
}
