package rules_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

var methodAndKeyRules = obj{"unused-methods": "error", "unused-object-keys": "error"}

type ruleMessage struct {
	Rule    string
	Message string
}

func ruleMessages(results []lint.Result) []ruleMessage {
	var out []ruleMessage
	for _, r := range results {
		out = append(out, ruleMessage{r.Rule, r.Message})
	}
	return out
}

func TestIgnoreComments(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{"fixture.ts": `
// slop-police-ignore unused-object-keys
export const ignoredObject = { a: 1, b: 2 };
export const ignoredKey = {
  a: 1,
  // slop-police-ignore
  b: 2,
};

class Store {
  // slop-police-ignore unused-methods -- kept for debugging
  debug() {}
  // slop-police-ignore unused-object-keys
  wrongRule() {}
}

// slop-police-ignore unused-methods
class IgnoredStore {
  unused() {}
}

new Store();
new IgnoredStore();
export const all = [ignoredObject.a, ignoredKey.a];
`}, obj{"rules": methodAndKeyRules})
	want := []ruleMessage{{"unused-methods", "Method Store.wrongRule is never used"}}
	if got := ruleMessages(results); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestUsagesAcrossProjects(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{
		"api/tsconfig.json": tsconfig,
		"api/store.ts": `
export class Store {
  usedByWeb() {}
  unused() {}
}
export const links = { usedByWeb: "a", unused: "b" };
`,
		"web/tsconfig.json": tsconfig,
		"web/view.ts": `
import { Store, links } from "../api/store.ts";
new Store().usedByWeb();
export const link = links.usedByWeb;
`,
	}, obj{"projects": []string{"api/tsconfig.json", "web/tsconfig.json"}, "rules": methodAndKeyRules})
	want := []ruleMessage{
		{"unused-methods", "Method Store.unused is never used"},
		{"unused-object-keys", "links.unused is never read"},
	}
	if got := ruleMessages(results); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestConfigErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		config string
		want   string
	}{
		{`{"projects": ["tsconfig.json"], "rules": {"unused-method": "error"}}`, "Unknown rule: unused-method"},
		{`{"projects": ["tsconfig.json"], "rules": {"unused-methods": ["error", {"private": true}]}}`, "Unknown option for rule unused-methods: private"},
		{`{"projects": ["tsconfig.json"], "rules": {"unused-methods": "fatal"}}`, `Rule unused-methods severity must be "error", "warn" or "off"`},
		{`{"projects": ["tsconfig.json"], "rules": {"unused-methods": ["error", {"includePrivate": 1}]}}`, "Rule unused-methods option includePrivate must be a boolean"},
		{`{"projects": [], "rules": {}}`, `"projects" must be a non-empty array of tsconfig paths`},
		{`{"projects": ["tsconfig.json"], "rule": {}}`, "Unknown config key: rule"},
		{`[]`, "Config must be an object"},
		{`null`, "Config must be an object"},
	}
	for _, test := range tests {
		_, err := lint.ParseConfig([]byte(test.config), nil)
		if err == nil || err.Error() != test.want {
			t.Errorf("%s: got error %v, want %q", test.config, err, test.want)
		}
	}
}

func TestDefaultRules(t *testing.T) {
	t.Parallel()
	all := defaultRules(t)
	if len(all) < 10 {
		t.Fatalf("got %d default rules", len(all))
	}
	for _, rule := range all {
		if rule.Severity != "error" || fmt.Sprint(rule.Options) != fmt.Sprint(rule.Rule.DefaultOptions) {
			t.Errorf("%s: got severity %s, options %v", rule.Rule.Name, rule.Severity, rule.Options)
		}
	}
	config, err := lint.ParseConfig([]byte(`{"projects": ["tsconfig.json"], "rules": {"unused-methods": "off", "no-duplicate-code": ["warn", {"minLines": 6}]}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, rule := range config.Rules {
		got[rule.Rule.Name] = fmt.Sprint(rule.Severity, " ", rule.Options["minLines"])
	}
	if _, ok := got["unused-methods"]; ok || len(got) != len(all)-1 || got["no-duplicate-code"] != "warn 6" || got["unused-object-keys"] != "error <nil>" {
		t.Errorf("got %v", got)
	}
}

func TestPluginConfig(t *testing.T) {
	t.Parallel()
	pluginRules := []*lint.Rule{{Name: "no-direct-fetch", DefaultOptions: lint.Options{"allow": []any{}}}}
	config, err := lint.ParseConfig([]byte(`{"projects": ["tsconfig.json"], "plugins": ["./rules.ts"], "rules": {"no-direct-fetch": ["warn", {"allow": ["src/api"]}]}}`), pluginRules)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(config.Rules, func(rule lint.EnabledRule) bool { return rule.Rule == pluginRules[0] })
	if i < 0 || config.Rules[i].Severity != "warn" || fmt.Sprint(config.Rules[i].Options) != "map[allow:[src/api]]" {
		t.Errorf("got rules %+v", config.Rules)
	}
	// A plugin rule that isn't listed doesn't run.
	config, err = lint.ParseConfig([]byte(`{"projects": ["tsconfig.json"], "plugins": ["./rules.ts"]}`), pluginRules)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(config.Rules, func(rule lint.EnabledRule) bool { return rule.Rule == pluginRules[0] }) {
		t.Errorf("unlisted plugin rule runs: %+v", config.Rules)
	}

	tests := []struct {
		config      string
		pluginRules []*lint.Rule
		want        string
	}{
		{`{"projects": ["tsconfig.json"], "plugins": "./rules.ts"}`, pluginRules, `"plugins" must be an array of module paths`},
		{`{"projects": ["tsconfig.json"], "plugins": ["./rules.ts"], "rules": {"no-direct-fetch": "error"}}`, nil, "Plugins are loaded by the npm launcher: run slop-police with npx or pnpm exec"},
		{`{"projects": ["tsconfig.json"], "plugins": ["./rules.ts"]}`, []*lint.Rule{{Name: "unused-methods"}}, "Plugin rule unused-methods has the same name as a built-in rule"},
		{`{"projects": ["tsconfig.json"], "plugins": ["./rules.ts"], "rules": {"no-direct-fetch": ["error", {"allow": "src/api"}]}}`, pluginRules, "Rule no-direct-fetch option allow must be an array"},
		{`{"projects": ["tsconfig.json"], "plugins": ["./rules.ts"], "rules": {"no-direct-fetch": ["error", {"deny": []}]}}`, pluginRules, "Unknown option for rule no-direct-fetch: deny"},
	}
	for _, test := range tests {
		_, err := lint.ParseConfig([]byte(test.config), test.pluginRules)
		if err == nil || err.Error() != test.want {
			t.Errorf("%s: got error %v, want %q", test.config, err, test.want)
		}
	}
}

func TestRunExitCodes(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, map[string]string{"store.ts": `
class Store {
  unused() {}
}
new Store();
`})
	run := func(config string) (int, string, string) {
		if err := os.WriteFile(filepath.Join(root, "slop-police.json"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := lint.Run(root, &stdout, &stderr, nil)
		return code, stdout.String(), stderr.String()
	}
	withSeverity := func(severity string) string {
		data, _ := json.Marshal(obj{"projects": []string{"tsconfig.json"}, "rules": obj{"unused-methods": severity}})
		return string(data)
	}

	if code, stdout, _ := run(withSeverity("error")); code != 1 || stdout != "store.ts:3:3  error  unused-methods  Method Store.unused is never used\n\n1 error, 0 warnings\n" {
		t.Errorf("error: exit %d, stdout %q", code, stdout)
	}
	if code, stdout, _ := run(withSeverity("warn")); code != 0 || !strings.Contains(stdout, "0 errors, 1 warning") {
		t.Errorf("warn: exit %d, stdout %q", code, stdout)
	}
	if code, stdout, _ := run(withSeverity("off")); code != 0 || stdout != "No problems found\n" {
		t.Errorf("off: exit %d, stdout %q", code, stdout)
	}
	if code, stdout, stderr := run(`{"projects": ["missing.json"], "rules": {"unused-methods": "error"}}`); code != 2 || stdout != "" || stderr != "TypeScript project not found: missing.json\n" {
		t.Errorf("missing project: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if code, _, stderr := run(`{"projects": `); code != 2 || !strings.HasPrefix(stderr, "Unable to parse slop-police.json: ") {
		t.Errorf("invalid json: exit %d, stderr %q", code, stderr)
	}
}

func TestGitignore(t *testing.T) {
	t.Parallel()
	alias := "type User = { id: string };\nexport type Member = User;\n"
	files := map[string]string{
		".gitignore":        "# build output\ndist/\n*.gen.ts\n!keep.gen.ts\n/local.ts\n",
		"sub/.gitignore":    "secret.ts\n",
		"src/a.ts":          alias,
		"src/keep.gen.ts":   alias,
		"src/routes.gen.ts": alias,
		"src/local.ts":      alias,
		"src/secret.ts":     alias,
		"dist/b.ts":         alias,
		"dist/keep.gen.ts":  alias, // a file in an ignored directory can't be included again
		"local.ts":          alias,
		"sub/secret.ts":     alias,
	}
	var got []string
	for _, r := range lintFindings(t, files, obj{"rules": obj{"no-identity-type-alias": "error"}}) {
		got = append(got, r.File)
	}
	slices.Sort(got)
	want := []string{"src/a.ts", "src/keep.gen.ts", "src/local.ts", "src/secret.ts"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestIgnoreGlobs(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"tsconfig.json":            strings.Replace(tsconfig, `["**/*.ts", "**/*.tsx"]`, `["src/**/*.ts", ".next/types/**/*.ts"]`, 1),
		"src/types.ts":             "type User = { id: string };\nexport type Member = User;",
		".next/types/validator.ts": "type Check = { ok: true };\nexport type Alias = Check;",
	}
	rules := obj{"no-identity-type-alias": "error"}

	if got := lintFindings(t, files, obj{"rules": rules}); len(got) != 2 {
		t.Errorf("without ignore: got %v, want 2 findings", got)
	}
	got := ruleMessages(lintFindings(t, files, obj{"rules": rules, "ignore": []string{"**/.next/**"}}))
	want := []ruleMessage{{"no-identity-type-alias", "Type alias Member only renames User"}}
	if !slices.Equal(got, want) {
		t.Errorf("with ignore: got %v, want %v", got, want)
	}
}

func TestUnresolvedImports(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, map[string]string{
		"globals.d.ts": `declare module "*.svg" { const url: string; export default url; }
declare module "virtual:config" { export const apiUrl: string; }
`,
		"util.ts": "export const one = 1;\n",
		"app.tsx": `import { useState } from "react";
import "./styles.css";
import logo from "./logo.svg";
import { apiUrl } from "virtual:config";
import { one } from "./util";
import { two } from "./missing";
export * from "lodash";
export type Store = import("mobx").IObservableValue<number>;
export const load = () => import("zod");
export const react = useState;
`,
		"other.ts": `import { useEffect } from "react";
export const effect = useEffect;
`,
	})
	_, err := lint.Lint(root, fixtureConfig(t, obj{"rules": obj{"unused-methods": "error"}}), nil)
	want := `Cannot find 5 imported modules; install dependencies or fix the imports:
  react  app.tsx:1:26
  ./missing  app.tsx:6:21
  lodash  app.tsx:7:15
  mobx  app.tsx:8:28
  zod  app.tsx:9:34`
	if err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
}
