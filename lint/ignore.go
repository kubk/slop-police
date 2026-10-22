package lint

import (
	"slices"
	"strings"
	"unicode"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
)

const ignoreDirective = "slop-police-ignore"

var commentFactory = ast.NewNodeFactory(ast.NodeFactoryHooks{})

// IsIgnored reports whether a `// slop-police-ignore [rules] [-- reason]`
// comment ends on the line of (or the line before) the node or one of its
// ancestors, up to the enclosing statement.
func IsIgnored(file *File, node *ast.Node, ruleName string) bool {
	for current := node; current != nil && current.Kind != ast.KindSourceFile; current = current.Parent {
		if hasIgnoreComment(file, current, ruleName) {
			return true
		}
		switch current.Parent.Kind {
		case ast.KindSourceFile, ast.KindBlock, ast.KindModuleBlock:
			return false
		}
	}
	return false
}

func hasIgnoreComment(file *File, node *ast.Node, ruleName string) bool {
	text := file.Source.Text()
	nodeLine := file.Line(Start(node, file.Source))
	for comment := range scanner.GetLeadingCommentRanges(commentFactory, text, node.Pos()) {
		if line := file.Line(comment.End()); line != nodeLine && line+1 != nodeLine {
			continue
		}
		rules, ok := parseIgnoredRules(text[comment.Pos():comment.End()])
		if ok && (len(rules) == 0 || slices.Contains(rules, ruleName)) {
			return true
		}
	}
	return false
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
