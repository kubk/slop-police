package rules

import (
	"fmt"
	"hash/maphash"
	"slices"
	"strings"
	"unicode"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// no-duplicate-code reports copy-pasted code the way jscpd does: a run of at
// least minTokens tokens that repeats earlier code, spanning at least
// minLines lines. Tokens are compared by text, without comments, formatting
// or imports.
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
		DefaultOptions: lint.Options{"minLines": 11.0, "minTokens": 50.0},
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

// tokenizeCode returns the tokens of the file's code.
func tokenizeCode(file *lint.File, seed maphash.Seed) []codeToken {
	text := file.Source.Text()
	t := &codeTokenizer{file: file, seed: seed, scanner: scanner.NewScanner(), hasIgnores: strings.Contains(text, "slop-police-ignore")}
	t.scanner.SetText(text)
	t.scanner.SetLanguageVariant(file.Source.LanguageVariant)
	t.node(file.Source.AsNode())
	return t.tokens
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
