package rules

import (
	"regexp"

	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// placeholderPattern matches filler that gestures at future work without
// explaining the code. The phrases are deliberately strong, so ordinary TODOs
// and maintenance notes don't match.
var placeholderPattern = regexp.MustCompile(`(?i)add\s+more\s+validation` +
	`|handle\s+(?:additional|more)\s+cases?` +
	`|can\s+be\s+extended\s+in\s+the\s+future` +
	`|extend\s+this\s+(?:logic|function|method|handler|module)` +
	`|customize\s+this\s+(?:logic|behavior|function|method|handler)` +
	`|future\s+enhancement` +
	`|implement\s+.+\s+here`)

func init() {
	lint.Register(&lint.Rule{
		Name: "no-placeholder-comment",
		// Comments aren't nodes, so this reports at text positions, which
		// only Check findings can do.
		Check: func(ctx *lint.Context, _ lint.Options) []lint.Finding {
			perFile := make([][]lint.Finding, len(ctx.Files))
			ctx.Parallel(len(ctx.Files), func(i int) {
				file := ctx.Files[i]
				text := file.Source.Text()
				forEachComment(file.Source, func(pos, end int) {
					if match := placeholderPattern.FindString(text[pos:end]); match != "" {
						perFile[i] = append(perFile[i], lint.Finding{File: file, Pos: pos, Message: `Comment only promises future work: "` + match + `"`})
					}
				})
			})
			var findings []lint.Finding
			for _, f := range perFile {
				findings = append(findings, f...)
			}
			return findings
		},
	})
}
