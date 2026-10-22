package rules

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

func init() {
	lint.Register(&lint.Rule{
		Name: "no-identity-type-alias",
		Visit: func(file *lint.File, node *ast.Node, _ lint.Options, report lint.Report) {
			if node.Kind != ast.KindTypeAliasDeclaration {
				return
			}
			aliased := node.AsTypeAliasDeclaration().Type
			for aliased != nil && aliased.Kind == ast.KindParenthesizedType {
				aliased = aliased.AsParenthesizedTypeNode().Type
			}
			if aliased == nil || aliased.Kind != ast.KindTypeReference || aliased.AsTypeReferenceNode().TypeArguments != nil {
				return
			}
			name := node.Name()
			report(name, "Type alias "+name.Text()+" only renames "+lint.Text(aliased, file.Source))
		},
	})
}
