package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

func init() {
	lint.Register(&lint.Rule{
		Name: "no-single-string-cn",
		Visit: func(file *lint.File, node *ast.Node, _ lint.Options, report lint.Report) {
			if node.Kind != ast.KindCallExpression {
				return
			}
			call := node.AsCallExpression()
			if call.Expression.Kind != ast.KindIdentifier || call.Expression.Text() != "cn" || len(nodeList(call.Arguments)) != 1 {
				return
			}
			argument := unwrapParentheses(call.Arguments.Nodes[0])
			if argument.Kind != ast.KindStringLiteral && argument.Kind != ast.KindNoSubstitutionTemplateLiteral {
				return
			}
			report(call.Expression, "Use the class string directly instead of wrapping it in cn()")
		},
	})
}
