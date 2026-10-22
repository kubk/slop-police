package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

func init() {
	lint.Register(&lint.Rule{
		Name: "no-pass-through-getter",
		Visit: func(file *lint.File, node *ast.Node, _ lint.Options, report lint.Report) {
			target := passThroughGetterTarget(node)
			if target == nil {
				return
			}
			name := node.Name()
			report(name, "Getter "+lint.Text(name, file.Source)+" only returns "+lint.Text(target, file.Source))
		},
	})
}

func passThroughGetterTarget(node *ast.Node) *ast.Node {
	if node.Kind != ast.KindGetAccessor {
		return nil
	}
	body := node.Body()
	if body == nil {
		return nil
	}
	statements := body.Statements()
	if len(statements) != 1 {
		return nil
	}
	statement := statements[0]
	if statement.Kind != ast.KindReturnStatement || statement.AsReturnStatement().Expression == nil {
		return nil
	}
	expression := unwrapParentheses(statement.AsReturnStatement().Expression)
	switch expression.Kind {
	case ast.KindPropertyAccessExpression:
		if expression.AsPropertyAccessExpression().Expression.Kind == ast.KindThisKeyword {
			return expression
		}
	case ast.KindElementAccessExpression:
		if expression.AsElementAccessExpression().Expression.Kind == ast.KindThisKeyword {
			return expression
		}
	}
	return nil
}
