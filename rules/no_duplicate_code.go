package rules

import (
	"fmt"
	"hash/maphash"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// no-duplicate-code reports copy-pasted code the way jscpd does (with
// --cross-formats js-ts): a run of at least minTokens tokens that repeats
// earlier code, spanning at least minLines lines. Tokens are compared by
// text, without comments, formatting, imports or TypeScript-only syntax, so
// code that only differs in its types is still a copy, and duplicated types
// aren't. Files in two different isolate folders (packages that can't share
// code) aren't compared.
const (
	duplicateCodeRule = "no-duplicate-code"
	// maxCopyCandidates bounds how many earlier windows with the same tokens
	// a window is compared with, for code repeated over and over.
	maxCopyCandidates = 16
	// windowBase is the multiplier of the rolling window hash (jscpd's).
	windowBase = 0x9e3779b97f4a7c15
)

func init() {
	lint.Register(&lint.Rule{
		Name:           duplicateCodeRule,
		DefaultOptions: lint.Options{"minLines": 10.0, "minTokens": 50.0, "isolate": []string{}},
		Check:          checkDuplicateCode,
	})
}

// codeToken is a token of a file's code: the hash of its text and its byte
// range. A zero-width token stands in for code silenced by an ignore
// comment, and matches nothing.
type codeToken struct {
	hash     uint64
	pos, end int
}

// tokenWindow is where a window of tokens starts: a file (by index into the
// tokenized files) and a token in it.
type tokenWindow struct {
	file, token int
}

func checkDuplicateCode(ctx *lint.Context, options lint.Options) []lint.Finding {
	minTokens := max(1, int(options.Number("minTokens")))
	minLines := int(options.Number("minLines"))
	var files []*lint.File
	for _, file := range ctx.Files {
		if !file.IsTest {
			files = append(files, file)
		}
	}
	// The copy that comes first by path is the one the others duplicate.
	slices.SortFunc(files, func(a, b *lint.File) int { return strings.Compare(a.RelPath, b.RelPath) })
	folders := make([]int, len(files)) // index of the isolate folder, or -1
	for i, file := range files {
		folders[i] = isolateFolder(options.Strings("isolate"), file.RelPath)
	}
	seed := maphash.MakeSeed()
	tokens := make([][]codeToken, len(files))
	ctx.Parallel(len(files), func(i int) { tokens[i] = tokenizeCode(files[i], seed) })

	var findings []lint.Finding
	report := func(copied, original tokenWindow, length int) {
		file := files[copied.file]
		start, end := tokenLines(file, tokens[copied.file], copied.token, length)
		originalStart, originalEnd := tokenLines(files[original.file], tokens[original.file], original.token, length)
		// Either copy may be formatted on fewer lines.
		if max(end-start, originalEnd-originalStart)+1 < minLines {
			return
		}
		where := "lines "
		if original.file != copied.file {
			where = files[original.file].RelPath + ":"
		}
		message := fmt.Sprintf("Lines %d-%d duplicate %s%d-%d", start+1, end+1, where, originalStart+1, originalEnd+1)
		findings = append(findings, lint.Finding{File: file, Pos: tokens[copied.file][copied.token].pos, Message: message})
	}

	// As in jscpd, a copy starts at a window of minTokens tokens that
	// appeared before, and it extends for as long as the tokens after both
	// match. Of the earlier windows with the same tokens (chained by hash),
	// the copy is of the one it matches longest, and of the earliest of
	// those.
	power := uint64(1)
	for range minTokens - 1 {
		power *= windowBase
	}
	none := tokenWindow{-1, -1}
	last := map[uint64]tokenWindow{}
	previous := make([][]tokenWindow, len(tokens)) // the window before with the same hash
	for fi, fileTokens := range tokens {
		previous[fi] = make([]tokenWindow, max(0, len(fileTokens)-minTokens+1))
		var hash uint64
		copyEnd := 0
		for i := range previous[fi] {
			if i == 0 {
				for _, token := range fileTokens[:minTokens] {
					hash = hash*windowBase + token.hash
				}
			} else {
				hash = (hash-fileTokens[i-1].hash*power)*windowBase + fileTokens[i+minTokens-1].hash
			}
			current := tokenWindow{fi, i}
			earlier, ok := last[hash]
			last[hash] = current
			if !ok {
				previous[fi][i] = none
				continue
			}
			previous[fi][i] = earlier
			if i+minTokens <= copyEnd {
				continue
			}
			original, length := none, 0
			for candidate, n := earlier, 0; candidate != none && n < maxCopyCandidates; candidate, n = previous[candidate.file][candidate.token], n+1 {
				if f := folders[candidate.file]; f >= 0 && folders[fi] >= 0 && f != folders[fi] {
					continue
				}
				if l := matchLength(tokens, candidate, current); l >= length {
					original, length = candidate, l
				}
			}
			// Shorter matches are hash collisions.
			if length >= minTokens {
				report(current, original, length)
				copyEnd = i + length
			}
		}
	}
	return findings
}

// isolateFolder returns the index of the folder that contains the file at
// relPath, or -1.
func isolateFolder(folders []string, relPath string) int {
	for i, folder := range folders {
		if strings.HasPrefix(relPath, path.Clean(folder)+"/") {
			return i
		}
	}
	return -1
}

// matchLength returns how many tokens from a and b are the same.
func matchLength(tokens [][]codeToken, a, b tokenWindow) int {
	at, bt := tokens[a.file][a.token:], tokens[b.file][b.token:]
	length := 0
	for length < len(at) && length < len(bt) && at[length].hash == bt[length].hash {
		length++
	}
	return length
}

// tokenLines returns the 0-based first and last line of length tokens from
// start.
func tokenLines(file *lint.File, tokens []codeToken, start, length int) (int, int) {
	return file.Line(tokens[start].pos), file.Line(tokens[start+length-1].end)
}

// tokenizeCode returns the tokens of the file's code, without
// TypeScript-only syntax (see typeOnlyRanges).
func tokenizeCode(file *lint.File, seed maphash.Seed) []codeToken {
	text := file.Source.Text()
	t := &codeTokenizer{file: file, seed: seed, scanner: scanner.NewScanner(), hasIgnores: strings.Contains(text, "slop-police-ignore")}
	t.scanner.SetText(text)
	t.scanner.SetLanguageVariant(file.Source.LanguageVariant)
	t.node(file.Source.AsNode())
	return withoutRanges(t.tokens, typeOnlyRanges(file.Source, t.tokens))
}

type codeTokenizer struct {
	file       *lint.File
	seed       maphash.Seed
	scanner    *scanner.Scanner
	hasIgnores bool
	tokens     []codeToken
}

func (t *codeTokenizer) add(text string, pos, end int) {
	t.tokens = append(t.tokens, codeToken{maphash.String(t.seed, text), pos, end})
}

// node adds the tokens of node: a token node is one token, and other nodes
// have their children's tokens and, in between, the punctuation and keywords
// the scanner finds there. Whatever needs the parser to scan (JSX text,
// regular expressions, template literals) is a token node.
func (t *codeTokenizer) node(node *ast.Node) {
	sf := t.file.Source
	if t.hasIgnores && node.Kind != ast.KindSourceFile && lint.HasIgnoreComment(t.file, node, duplicateCodeRule) {
		pos := lint.Start(node, sf)
		t.tokens = append(t.tokens, codeToken{maphash.Comparable(t.seed, tokenWindow{t.file.Index, pos}), pos, pos})
		return
	}
	text := sf.Text()
	switch {
	case node.Kind == ast.KindImportDeclaration || node.Kind == ast.KindImportEqualsDeclaration || node.Kind == ast.KindExportDeclaration:
		// Files that use the same modules import them the same way.
	case node.Kind == ast.KindJsxText:
		// JSX trims lines of text, so indentation doesn't count.
		raw := text[node.Pos():node.End()]
		if words := strings.Fields(raw); len(words) > 0 {
			t.add(strings.Join(words, " "), node.Pos()+strings.IndexFunc(raw, isNotSpace), node.Pos()+strings.LastIndexFunc(raw, isNotSpace)+1)
		}
	case ast.IsTokenKind(node.Kind):
		if pos := lint.Start(node, sf); pos < node.End() {
			t.add(text[pos:node.End()], pos, node.End())
		}
	default:
		pos := node.Pos()
		node.ForEachChild(func(child *ast.Node) bool {
			t.gap(pos, child.Pos())
			t.node(child)
			pos = child.End()
			return false
		})
		t.gap(pos, node.End())
	}
}

// isNotSpace is for the ASCII whitespace that ends JSX text, so that
// LastIndexFunc's index plus one is the end.
func isNotSpace(r rune) bool {
	return r > unicode.MaxASCII || !unicode.IsSpace(r)
}

// gap adds the tokens in [pos, end), text between child nodes.
func (t *codeTokenizer) gap(pos, end int) {
	if pos >= end {
		return
	}
	text := t.file.Source.Text()
	t.scanner.ResetPos(pos)
	for t.scanner.Scan() != ast.KindEndOfFile && t.scanner.TokenStart() < end {
		start := t.scanner.TokenStart()
		tokenEnd := min(t.scanner.TokenEnd(), end)
		t.add(text[start:tokenEnd], start, tokenEnd)
	}
}

// typeOnlyRanges returns the byte ranges of the TypeScript syntax that
// compiling to JavaScript erases, which jscpd's js-ts mode drops: types with
// their colons and angle brackets, type declarations, overload signatures,
// ambient declarations, `this` parameters, `as T`, `satisfies T`, `!`, `?`
// and TypeScript modifiers (but not those of parameter properties, which
// assign fields). tokens are the file's tokens, for the punctuation around
// types.
func typeOnlyRanges(sf *ast.SourceFile, tokens []codeToken) []core.TextRange {
	text := sf.Text()
	var ranges []core.TextRange
	add := func(pos, end int) {
		if pos < end {
			ranges = append(ranges, core.NewTextRange(pos, end))
		}
	}
	// tokenAt returns the index of the first token at or after pos.
	tokenAt := func(pos int) int {
		i, _ := slices.BinarySearchFunc(tokens, pos, func(token codeToken, pos int) int { return token.pos - pos })
		return i
	}
	tokenText := func(i int) string {
		if i < 0 || i >= len(tokens) {
			return ""
		}
		return text[tokens[i].pos:tokens[i].end]
	}
	start := func(node *ast.Node) int { return lint.Start(node, sf) }

	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		switch {
		case isTypeOnlyNode(node):
			add(start(node), node.End())
			return false
		case ast.IsTypeNode(node) && node.Kind != ast.KindExpressionWithTypeArguments:
			pos := start(node)
			if i := tokenAt(pos); tokenText(i-1) == ":" {
				pos = tokens[i-1].pos
			}
			add(pos, node.End())
			return false
		case ast.IsThisParameter(node):
			end := node.End()
			if i := tokenAt(end); tokenText(i) == "," {
				end = tokens[i].end
			}
			add(start(node), end)
			return false
		}
		switch node.Kind {
		case ast.KindAsExpression, ast.KindSatisfiesExpression, ast.KindNonNullExpression:
			add(node.Expression().End(), node.End())
		case ast.KindTypeAssertionExpression:
			add(start(node), node.Expression().Pos())
		case ast.KindHeritageClause:
			if node.AsHeritageClause().Token == ast.KindImplementsKeyword {
				add(start(node), node.End())
				return false
			}
		}
		if list := typeArgumentsOrParameters(node); list != nil {
			if i, j := tokenAt(list.Pos()), tokenAt(list.End()); tokenText(i-1) == "<" && tokenText(j) == ">" {
				add(tokens[i-1].pos, tokens[j].end)
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(sf.AsNode())
	return ranges
}

func isTypeOnlyNode(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration, ast.KindIndexSignature, ast.KindTypeParameter:
		return true
	case ast.KindFunctionDeclaration, ast.KindMethodDeclaration, ast.KindConstructor, ast.KindGetAccessor, ast.KindSetAccessor:
		// Without a body: an overload signature or abstract member.
		return node.Body() == nil
	case ast.KindVariableStatement, ast.KindClassDeclaration, ast.KindEnumDeclaration, ast.KindModuleDeclaration, ast.KindPropertyDeclaration:
		return ast.HasSyntacticModifier(node, ast.ModifierFlagsAmbient)
	case ast.KindPublicKeyword, ast.KindPrivateKeyword, ast.KindProtectedKeyword, ast.KindReadonlyKeyword,
		ast.KindOverrideKeyword, ast.KindAbstractKeyword:
		return node.Parent.Kind != ast.KindParameter
	case ast.KindQuestionToken, ast.KindExclamationToken:
		// Optional and definite assignment markers, not a conditional's `?`.
		switch node.Parent.Kind {
		case ast.KindParameter:
			return !ast.IsParameterPropertyDeclaration(node.Parent, node.Parent.Parent)
		case ast.KindPropertyDeclaration, ast.KindMethodDeclaration, ast.KindVariableDeclaration:
			return true
		}
	}
	return false
}

func typeArgumentsOrParameters(node *ast.Node) *ast.NodeList {
	switch node.Kind {
	case ast.KindCallExpression, ast.KindNewExpression, ast.KindTaggedTemplateExpression, ast.KindExpressionWithTypeArguments,
		ast.KindJsxOpeningElement, ast.KindJsxSelfClosingElement:
		return node.TypeArgumentList()
	case ast.KindClassDeclaration, ast.KindClassExpression, ast.KindFunctionDeclaration, ast.KindFunctionExpression,
		ast.KindArrowFunction, ast.KindMethodDeclaration:
		return node.TypeParameterList()
	}
	return nil
}

// withoutRanges drops the tokens that overlap ranges. A zero-width token
// only overlaps a range it's inside of.
func withoutRanges(tokens []codeToken, ranges []core.TextRange) []codeToken {
	slices.SortFunc(ranges, func(a, b core.TextRange) int { return a.Pos() - b.Pos() })
	var merged []core.TextRange
	for _, r := range ranges {
		if last := len(merged) - 1; last >= 0 && r.Pos() <= merged[last].End() {
			merged[last] = core.NewTextRange(merged[last].Pos(), max(merged[last].End(), r.End()))
		} else {
			merged = append(merged, r)
		}
	}
	kept := tokens[:0]
	r := 0
	for _, token := range tokens {
		for r < len(merged) && merged[r].End() <= token.pos {
			r++
		}
		if r < len(merged) && merged[r].Pos() < token.end && token.pos < merged[r].End() {
			continue
		}
		kept = append(kept, token)
	}
	return kept
}
