package rules_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

var methodAndKeyRules = obj{"unused-class-members": "error", "unused-object-keys": "error"}

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
  // slop-police-ignore unused-class-members -- kept for debugging
  debug() {}
  // slop-police-ignore unused-object-keys
  wrongRule() {}
}

// slop-police-ignore unused-class-members
class IgnoredStore {
  unused() {}
}

new Store();
new IgnoredStore();
export const all = [ignoredObject.a, ignoredKey.a];
`}, obj{"rules": methodAndKeyRules})
	want := []ruleMessage{{"unused-class-members", "Method Store.wrongRule is never used"}}
	if got := ruleMessages(results); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestUnusedIgnoreComments(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{"fixture.ts": `
class Store {
  // slop-police-ignore unused-class-members -- called from the devtools console
  debug() {}
  // slop-police-ignore unused-class-members -- now called below
  load() {}
  // slop-police-ignore unused-class-members, unused-object-keys
  reset() {}
  // slop-police-ignore unused-class-members no-duplicate-code no-promise-default-fallback
  close() {}
}

// slop-police-ignore
export const links = { docs: "/docs" };

// slop-police-ignore

export const spaced = 1;

new Store().load();
export const all = [links.docs];
`}, obj{"rules": obj{"unused-class-members": "error", "unused-object-keys": "error", "unused-ignore": "warn"}})
	want := []ruleMessage{
		{"unused-ignore", "Ignore comment for unused-class-members silences nothing"},
		{"unused-ignore", "Ignore comment for unused-object-keys silences nothing"},
		{"unused-ignore", `Ignore comment names unknown rule "no-promise-default-fallback"`},
		{"unused-ignore", "Ignore comment silences nothing"},
		{"unused-ignore", "Ignore comment silences nothing"},
	}
	if got := ruleMessages(results); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if results[0].Line != 5 || results[0].Severity != "warn" {
		t.Errorf("got %+v, want line 5 as a warning", results[0])
	}
}

func TestUsagesAcrossProjects(t *testing.T) {
	t.Parallel()
	checkUsagesAcrossProjects(t)
}

// Not parallel: it changes how every run takes checkers. With one worker
// every project is a batch, and without a memory budget a batch releases
// the checkers of the one before, so every pass takes its checkers again.
func TestUsagesAcrossProjectsWithReleasedCheckers(t *testing.T) {
	workers, budget := lint.Workers, lint.MemoryBudget
	lint.Workers, lint.MemoryBudget = 1, 0
	defer func() { lint.Workers, lint.MemoryBudget = workers, budget }()
	checkUsagesAcrossProjects(t)
}

// Not parallel: it measures the heap. A run's programs used to stay reachable
// after it, which added up to gigabytes over this package's tests.
func TestRunsAreCollected(t *testing.T) {
	checkUsagesAcrossProjects(t) // allocates what's allocated once
	before := liveHeap()
	const runs = 8
	for range runs {
		checkUsagesAcrossProjects(t)
	}
	// A run that stays reachable keeps more than 20 MB.
	if grown := (int64(liveHeap()) - int64(before)) >> 20; grown > 50 {
		t.Errorf("the heap grew by %d MB over %d runs", grown, runs)
	}
}

// liveHeap is the size of the heap's reachable objects.
func liveHeap() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func checkUsagesAcrossProjects(t *testing.T) {
	t.Helper()
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
		{"unused-class-members", "Method Store.unused is never used"},
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
		{`{"projects": ["tsconfig.json"], "rules": {"unused-class-members": ["error", {"private": true}]}}`, "Unknown option for rule unused-class-members: private"},
		{`{"projects": ["tsconfig.json"], "rules": {"unused-class-members": "fatal"}}`, `Rule unused-class-members severity must be "error", "warn" or "off"`},
		{`{"projects": ["tsconfig.json"], "rules": {"unused-class-members": ["error", {"includePrivate": 1}]}}`, "Rule unused-class-members option includePrivate must be a boolean"},
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
	rulesOf := func(rules string) map[string]string {
		config, err := lint.ParseConfig([]byte(`{"projects": ["tsconfig.json"], "rules": `+rules+`}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, rule := range config.Rules {
			got[rule.Rule.Name] = fmt.Sprint(rule.Severity, " ", rule.Options["minLines"])
		}
		return got
	}
	got := rulesOf(`{"unused-exports": "off", "no-duplicate-code": ["warn", {"minLines": 6}]}`)
	if _, ok := got["unused-exports"]; ok || len(got) != len(all)-1 || got["no-duplicate-code"] != "warn 6" || got["unused-object-keys"] != "error <nil>" {
		t.Errorf("got %v", got)
	}
	// An optional rule runs only when listed.
	if _, ok := got["unused-class-members"]; ok {
		t.Errorf("unused-class-members runs by default: %v", got)
	}
	if got := rulesOf(`{"unused-class-members": "warn"}`); len(got) != len(all)+1 || got["unused-class-members"] != "warn <nil>" {
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
		{`{"projects": ["tsconfig.json"], "plugins": ["./rules.ts"]}`, []*lint.Rule{{Name: "unused-class-members"}}, "Plugin rule unused-class-members has the same name as a built-in rule"},
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

// crashRule panics in files that mention it, to test crash reports.
func init() {
	lint.Register(&lint.Rule{
		Name: "test-crash",
		CheckFile: func(file *lint.File, _ *checker.Checker, _ lint.Options, _ lint.Report) {
			if strings.Contains(file.Source.Text(), "test-crash") {
				panic("boom")
			}
		},
	})
}

func TestCrash(t *testing.T) {
	t.Parallel()
	root := writeFixture(t, map[string]string{
		"ok.ts":            "export const a = 1;\n",
		"src/bad.ts":       "// test-crash\nexport const b = 1;\n",
		"slop-police.json": `{"projects": ["tsconfig.json"], "rules": {"test-crash": "error", "unused-exports": "error"}}`,
	})
	var stdout, stderr bytes.Buffer
	code := lint.Run(root, &stdout, &stderr, nil)
	if code != 3 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "slop-police crashed in test-crash on src/bad.ts: boom\n") || !strings.Contains(stderr.String(), "rules_test.init") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
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
		data, _ := json.Marshal(obj{"projects": []string{"tsconfig.json"}, "rules": obj{"unused-class-members": severity}})
		return string(data)
	}

	if code, stdout, _ := run(withSeverity("error")); code != 1 || stdout != "store.ts:3:3  error  unused-class-members  Method Store.unused is never used\n\n1 error, 0 warnings\n" {
		t.Errorf("error: exit %d, stdout %q", code, stdout)
	}
	if code, stdout, _ := run(withSeverity("warn")); code != 0 || !strings.Contains(stdout, "0 errors, 1 warning") {
		t.Errorf("warn: exit %d, stdout %q", code, stdout)
	}
	if code, stdout, _ := run(withSeverity("off")); code != 0 || stdout != "No problems found\n" {
		t.Errorf("off: exit %d, stdout %q", code, stdout)
	}
	if code, stdout, stderr := run(`{"projects": ["missing.json"], "rules": {"unused-class-members": "error"}}`); code != 2 || stdout != "" || stderr != "TypeScript project not found: missing.json\n" {
		t.Errorf("missing project: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if code, _, stderr := run(`{"projects": `); code != 2 || !strings.HasPrefix(stderr, "Unable to parse slop-police.json: ") {
		t.Errorf("invalid json: exit %d, stderr %q", code, stderr)
	}
	if err := os.Remove(filepath.Join(root, "slop-police.json")); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := lint.Run(root, &stdout, &stderr, nil); code != 2 || !strings.Contains(stderr.String(), "/INSTALL.md") {
		t.Errorf("missing config: exit %d, stderr %q", code, stderr.String())
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

func TestTestFilesSkipPerFileRules(t *testing.T) {
	t.Parallel()
	source := `
function fetchUser(id: string) { return id; }
class Store {
  private transition = "animated";
  get currentTransition() { return this.transition; }
  load(id: string) { return fetchUser(id); }
}
type User = { id: string };
type Member = User;
function search(member: Member) {
  const store = new Store();
  const alias = store;
  return alias.load(member.id) + alias.currentTransition;
}
search({ id: "" });
export {};
`
	results := lintFindings(t, map[string]string{"src/store.ts": source, "tests/store.ts": source}, obj{
		"testFiles": []string{"tests/**"},
		"rules": obj{
			"no-pass-through-getter":   "error",
			"no-pass-through-wrapper":  "error",
			"no-identity-type-alias":   "error",
			"no-redundant-value-alias": "error",
		},
	})
	var got []string
	for _, r := range results {
		got = append(got, r.File+" "+r.Rule)
	}
	want := []string{
		"src/store.ts no-pass-through-getter",
		"src/store.ts no-pass-through-wrapper",
		"src/store.ts no-identity-type-alias",
		"src/store.ts no-redundant-value-alias",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
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
import { three } from "../lib/missing";
import photo from "./photo.png";
export * from "lodash";
export type Store = import("mobx").IObservableValue<number>;
export const load = () => import("zod");
export const react = useState;
`,
		"photo.png": "",
		"app.test.ts": `import { render } from "./gone";
export const rendered = render;
`,
		"other.ts": `import { useEffect } from "react";
export const effect = useEffect;
`,
		"virtual.ts": `// @ts-expect-error -- resolved by the bundler
import entry from "virtual:entry";
// @ts-ignore
// resolved by the runtime
import * as workers from "cloudflare:workers";
// @ts-expect-error
export const used = entry;
import { stage } from "virtual:stage";
// @ts-expect-error
import {
  manifest,
} from "virtual:manifest";
export const all = [workers, stage, manifest];
`,
	})
	_, err := lint.Lint(root, fixtureConfig(t, obj{"rules": obj{"unused-class-members": "error"}}), nil)
	want := `Cannot find 7 imported modules; install dependencies or fix the imports:
  react  app.tsx:1:26
  ./photo.png  app.tsx:8:19
  lodash  app.tsx:9:15
  mobx  app.tsx:10:28
  zod  app.tsx:11:34
  virtual:stage  virtual.ts:8:23
  virtual:manifest  virtual.ts:12:8`
	if err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
}

// A relative import of a file that doesn't exist is a finding: it can't hide
// usages, so the rest of the run is still sound.
func TestMissingImportedFile(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{
		"util.ts": "export const one = 1;\n",
		"app.ts": `import { one } from "./util";
import { two } from "./missing";
// slop-police-ignore no-unresolved-import -- generated at build time
import { three } from "./generated";
export const all = [one, two, three];
`,
		"app.test.ts": `import { render } from "../gone";
render();
`,
	}, obj{"rules": obj{"no-unresolved-import": "error"}})
	want := []lint.Result{
		{Rule: "no-unresolved-import", Severity: "error", File: "app.test.ts", Line: 1, Column: 24, Message: "Cannot find module ../gone"},
		{Rule: "no-unresolved-import", Severity: "error", File: "app.ts", Line: 2, Column: 21, Message: "Cannot find module ./missing"},
	}
	if !slices.Equal(results, want) {
		t.Errorf("got %v\nwant %v", results, want)
	}
}

func TestPublicAPI(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{
		"index.ts": `
export { Store, Mode, labels, type Item, format } from "./store.ts";
export * from "./graph.ts";
export * as internal from "./internal.ts";
`,
		"store.ts": `
class Base {
  inherited() {}
  close() {}
}
export class Store extends Base {
  close() { return super.close(); }
  private items = new Map<string, string>();
  get size() { return this.length; }
  get length() { return 0; }
  load(key: string) { return this.items.get(key); }
  reload(key: string) { return this.read(key); }
  private read(key: string) { return key; }
  private unusedPrivate() { return 1; }
}
export enum Mode { A, B }
export const labels = { a: "A", b: "B" };
type Inner = { id: string };
export type Item = Inner;
declare function formatValue(value: number): string;
export function format(value: number) { return formatValue(value); }
export class Unexported {
  unused() {}
}
`,
		"graph.ts": `
import { labels } from "./store.ts";
export class Graph {
  walk() { return labels.a; }
}
`,
		"internal.ts": `
export class Helper {
  help() {}
}
`,
	}, obj{
		"entry": []string{"index.ts"},
		"rules": obj{
			"unused-class-members":    []any{"error", obj{"includePrivate": true}},
			"no-pass-through-wrapper": "error",
			"no-pass-through-getter":  "error",
			"no-identity-type-alias":  "error",
			"unused-enum-members":     "error",
			"unused-object-keys":      "error",
		},
	})
	want := []ruleMessage{
		{"no-pass-through-wrapper", "Method close only forwards to super.close"},
		{"unused-class-members", "Method Store.unusedPrivate is never used"},
		{"unused-class-members", "Method Unexported.unused is never used"},
	}
	if got := ruleMessages(results); !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}
