package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// forEachComment calls fn with the range of every comment in sf, in source
// order. Comments are trivia, so they lie in the text between a node's
// children (or before a token's start), which holds only trivia and
// punctuation or keyword tokens, and can be scanned without the parser's
// context. The token text itself (strings, templates, regular expressions,
// JSX text) is never scanned, so `//` inside it isn't taken for a comment.
func forEachComment(sf *ast.SourceFile, fn func(pos, end int)) {
	s := scanner.NewScanner()
	s.SetText(sf.Text())
	s.SetSkipTrivia(false)
	scanGap := func(pos, end int) {
		if pos >= end {
			return
		}
		s.ResetPos(pos)
		for kind := s.Scan(); kind != ast.KindEndOfFile && s.TokenStart() < end; kind = s.Scan() {
			if (kind == ast.KindSingleLineCommentTrivia || kind == ast.KindMultiLineCommentTrivia) && s.TokenEnd() <= end {
				fn(s.TokenStart(), s.TokenEnd())
			}
		}
	}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsTokenKind(node.Kind) {
			scanGap(node.Pos(), scanner.GetTokenPosOfNode(node, sf, false))
			return false
		}
		pos := node.Pos()
		node.ForEachChild(func(child *ast.Node) bool {
			scanGap(pos, child.Pos())
			visit(child)
			pos = max(pos, child.End())
			return false
		})
		scanGap(pos, node.End())
		return false
	}
	visit(sf.AsNode())
}
