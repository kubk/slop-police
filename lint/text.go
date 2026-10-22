package lint

import (
	"unicode/utf16"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// Start is node.getStart(): its position after leading trivia (a byte offset).
func Start(node *ast.Node, sf *ast.SourceFile) int {
	return scanner.GetTokenPosOfNode(node, sf, false)
}

// Text is node.getText(): the node's source text without leading trivia.
func Text(node *ast.Node, sf *ast.SourceFile) string {
	return sf.Text()[Start(node, sf):node.End()]
}

// Line returns the 0-based line containing the byte offset pos.
func (f *File) Line(pos int) int {
	f.lineOnce.Do(func() { f.lineStarts = f.Source.ECMALineMap() })
	low, high := 0, len(f.lineStarts)-1
	for low < high {
		middle := (low + high + 1) / 2
		if int(f.lineStarts[middle]) <= pos {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return low
}

// LineAndColumn returns the 0-based line and character of pos. Characters
// are UTF-16 code units, like positions in TypeScript (and editors).
func (f *File) LineAndColumn(pos int) (int, int) {
	line := f.Line(pos)
	prefix := f.Source.Text()[f.lineStarts[line]:pos]
	return line, len(utf16.Encode([]rune(prefix)))
}

// UTF16Offset converts a byte offset to the UTF-16 offset TypeScript's JS API
// uses for positions.
func (f *File) UTF16Offset(pos int) int {
	return f.Source.GetPositionMap().UTF8ToUTF16(pos)
}
