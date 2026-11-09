package lint

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
)

const ignoreDirective = "slop-police-ignore"

// unusedIgnore reports ignore comments that silence nothing. It needs the
// other rules' findings, so collectResults runs it after them.
const unusedIgnore = "unused-ignore"

func init() {
	Register(&Rule{Name: unusedIgnore, Check: func(*Context, Options) []Finding { return nil }})
}

var commentFactory = ast.NewNodeFactory(ast.NodeFactoryHooks{})

// isIgnored returns the start of a `// slop-police-ignore [rules] [-- reason]`
// comment for ruleName that ends on the line of (or the line before) the node
// or one of its ancestors, up to the enclosing statement.
func isIgnored(file *File, node *ast.Node, ruleName string) (int, bool) {
	for current := node; current != nil && current.Kind != ast.KindSourceFile; current = current.Parent {
		if pos, ok := ignoreComment(file, current, ruleName); ok {
			return pos, true
		}
		switch current.Parent.Kind {
		case ast.KindSourceFile, ast.KindBlock, ast.KindModuleBlock:
			return 0, false
		}
	}
	return 0, false
}

// HasIgnoreComment is isIgnored for the node alone, without its ancestors.
func HasIgnoreComment(file *File, node *ast.Node, ruleName string) bool {
	_, ok := ignoreComment(file, node, ruleName)
	return ok
}

func ignoreComment(file *File, node *ast.Node, ruleName string) (int, bool) {
	text := file.Source.Text()
	nodeLine := file.Line(Start(node, file.Source))
	for comment := range scanner.GetLeadingCommentRanges(commentFactory, text, node.Pos()) {
		if line := file.Line(comment.End()); line != nodeLine && line+1 != nodeLine {
			continue
		}
		if ignores(text[comment.Pos():comment.End()], ruleName) {
			return comment.Pos(), true
		}
	}
	return 0, false
}

// isIgnoredAt is isIgnored for findings at a text position (such as a
// comment) rather than a node: the line before pos must be a
// `// slop-police-ignore [rules] [-- reason]` comment.
func isIgnoredAt(file *File, pos int, ruleName string) (int, bool) {
	line := file.Line(pos)
	if line == 0 {
		return 0, false
	}
	start := int(file.lineStarts[line-1])
	previous := file.Source.Text()[start:file.lineStarts[line]]
	trimmed := strings.TrimLeftFunc(previous, isSpace)
	return start + len(previous) - len(trimmed), ignores(strings.TrimRightFunc(trimmed, isSpace), ruleName)
}

// ignoreUse is a rule (empty for any) that an ignore comment silenced.
type ignoreUse struct {
	file *File
	pos  int
	rule string
}

// unusedIgnores reports the ignore comments of the files that silenced none
// of the findings in used: those without rules that silenced nothing, and
// for the others, the enabled rules they name that they silenced nothing of
// and the rules they name that don't exist.
func unusedIgnores(files []*File, known map[string]bool, enabled map[string]bool, used map[ignoreUse]bool) []Finding {
	var findings []Finding
	for _, file := range files {
		for _, comment := range ignoreComments(file) {
			pos := comment.Pos()
			rules, _ := parseIgnoredRules(file.Source.Text()[pos:comment.End()])
			if len(rules) == 0 {
				if !used[ignoreUse{file, pos, ""}] {
					findings = append(findings, Finding{File: file, Pos: pos, Message: "Ignore comment silences nothing"})
				}
				continue
			}
			var unused []string
			for _, rule := range rules {
				switch {
				case !known[rule]:
					findings = append(findings, Finding{File: file, Pos: pos, Message: "Ignore comment names unknown rule " + strconv.Quote(rule)})
				case enabled[rule] && !used[ignoreUse{file, pos, rule}]:
					unused = append(unused, rule)
				}
			}
			if len(unused) > 0 {
				findings = append(findings, Finding{File: file, Pos: pos, Message: "Ignore comment for " + strings.Join(unused, ", ") + " silences nothing"})
			}
		}
	}
	return findings
}

// ignoreComments returns the file's ignore comments, in order: the leading
// comments of its nodes that are `// slop-police-ignore` directives.
func ignoreComments(file *File) []ast.CommentRange {
	text := file.Source.Text()
	if !strings.Contains(text, ignoreDirective) {
		return nil
	}
	var comments []ast.CommentRange
	seen := map[int]bool{}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		for comment := range scanner.GetLeadingCommentRanges(commentFactory, text, node.Pos()) {
			if _, ok := parseIgnoredRules(text[comment.Pos():comment.End()]); ok && !seen[comment.Pos()] {
				seen[comment.Pos()] = true
				comments = append(comments, comment)
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(file.Source.AsNode())
	slices.SortFunc(comments, func(a, b ast.CommentRange) int { return a.Pos() - b.Pos() })
	return comments
}

func ignores(comment string, ruleName string) bool {
	rules, ok := parseIgnoredRules(comment)
	return ok && (len(rules) == 0 || slices.Contains(rules, ruleName))
}

func parseIgnoredRules(comment string) ([]string, bool) {
	body, ok := strings.CutPrefix(comment, "//")
	if !ok {
		return nil, false
	}
	body = strings.TrimFunc(body, isSpace)
	if body != ignoreDirective && !strings.HasPrefix(body, ignoreDirective+" ") {
		return nil, false
	}
	rules, _, _ := strings.Cut(body[len(ignoreDirective):], "--")
	return strings.FieldsFunc(rules, func(r rune) bool { return r == ',' || isSpace(r) }), true
}

// isSpace matches JavaScript's whitespace, which differs from Go's in U+FEFF
// and U+0085.
func isSpace(r rune) bool {
	return r == '\uFEFF' || r != '\u0085' && unicode.IsSpace(r)
}
