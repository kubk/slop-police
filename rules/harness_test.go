package rules_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/typescript-go/x/slop-police/lint"
	_ "github.com/microsoft/typescript-go/x/slop-police/rules"
)

type obj = map[string]any

const tsconfig = `{
  "compilerOptions": {
    "strict": true,
    "jsx": "preserve",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "allowImportingTsExtensions": true,
    "noEmit": true,
    "target": "ES2020"
  },
  "include": ["**/*.ts", "**/*.tsx"]
}`

type finding struct {
	Line    int
	Message string
}

func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{"tsconfig.json": tsconfig}
	maps.Copy(all, files)
	for name, content := range all {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func fixtureConfig(t *testing.T, config obj) *lint.Config {
	t.Helper()
	merged := obj{"projects": []string{"tsconfig.json"}, "testFiles": []string{"**/*.test.ts"}}
	maps.Copy(merged, config)
	data, err := json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := lint.ParseConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func lintFindings(t *testing.T, files map[string]string, config obj) []lint.Result {
	t.Helper()
	results, err := lint.Lint(writeFixture(t, files), fixtureConfig(t, config), nil)
	if err != nil {
		t.Fatal(err)
	}
	return results
}

func messages(results []lint.Result) []string {
	var out []string
	for _, r := range results {
		out = append(out, r.Message)
	}
	return out
}

func lineMessages(results []lint.Result) []finding {
	var out []finding
	for _, r := range results {
		out = append(out, finding{r.Line, r.Message})
	}
	return out
}

type locatedFinding struct {
	File    string
	Line    int
	Column  int
	Message string
}

func locatedFindings(results []lint.Result) []locatedFinding {
	var out []locatedFinding
	for _, r := range results {
		out = append(out, locatedFinding{r.File, r.Line, r.Column, r.Message})
	}
	return out
}
