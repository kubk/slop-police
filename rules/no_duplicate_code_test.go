package rules_test

import (
	"slices"
	"strings"
	"testing"
)

var duplicateCodeConfig = obj{"rules": obj{"no-duplicate-code": "error"}}

// loadFunction is 12 lines and about 100 tokens.
func loadFunction(name string) string {
	return "export function " + name + `(id: string) {
  const response = fetchJson("/users/" + id);
  if (!response.ok) {
    throw new Error("failed to load users");
  }
  const items = response.data.items.map((item) => item.value);
  const total = items.reduce((sum, value) => sum + value, 0);
  if (total > 100) {
    notify("too many users");
  }
  return items.filter((value) => value > 10);
}
`
}

// importingFiles returns a.ts and b.ts with source, plus the c.ts to k.ts
// they import.
func importingFiles(source string) map[string]string {
	files := map[string]string{"a.ts": source, "b.ts": source}
	for _, name := range strings.Split("cdefghijk", "") {
		files[name+".ts"] = "export const " + name + " = 1;\n"
	}
	return files
}

func TestNoDuplicateCode(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{
		"a.ts": loadFunction("loadUsers"),
		// The copy starts where the names stop differing.
		"b.ts": "const limit = 10;\n\n" + loadFunction("loadPosts"),
		"c.ts": strings.ReplaceAll(loadFunction("loadTeams"), "users", "teams") + "\n" + strings.ReplaceAll(loadFunction("loadTags"), "users", "teams"),
	}, duplicateCodeConfig)
	want := []locatedFinding{
		{"b.ts", 3, 26, "Lines 3-14 duplicate a.ts:1-12"},
		{"c.ts", 14, 25, "Lines 14-25 duplicate lines 1-12"},
	}
	if got := locatedFindings(results); !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestNoDuplicateCodeTypes(t *testing.T) {
	t.Parallel()
	settings := `export interface Settings {
  theme: "light" | "dark";
  language: string;
  notifications: boolean;
  fontSize: number;
  timezone: string;
  currency: string;
  dateFormat: string;
  weekStart: "monday" | "sunday";
  compactMode: boolean;
  soundEnabled: boolean;
  autoSave: boolean;
}
`
	results := lintFindings(t, map[string]string{
		// Repeated types are a copy too.
		"a.ts": settings,
		"b.ts": settings,
		// Types are compared like the rest of the code, so a different one in
		// the middle leaves two pieces that are too short.
		"c.ts": loadFunction("loadUsers"),
		"d.ts": strings.Replace(loadFunction("loadPosts"), "map((item)", "map((item: Item)", 1),
	}, duplicateCodeConfig)
	want := []locatedFinding{{"b.ts", 1, 1, "Lines 1-13 duplicate a.ts:1-13"}}
	if got := locatedFindings(results); !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestNoDuplicateCodeThresholds(t *testing.T) {
	t.Parallel()
	// 9 lines and 86 tokens, below the default of 11 lines.
	short := strings.Replace(loadFunction("load"), `  if (total > 100) {
    notify("too many users");
  }
`, "", 1)
	files := map[string]string{"a.ts": short, "b.ts": short}
	withOptions := func(options obj) obj {
		return obj{"rules": obj{"no-duplicate-code": []any{"error", options}}}
	}
	if got := lintFindings(t, files, duplicateCodeConfig); len(got) != 0 {
		t.Errorf("defaults: got %v", locatedFindings(got))
	}
	want := []locatedFinding{{"b.ts", 1, 1, "Lines 1-9 duplicate a.ts:1-9"}}
	if got := locatedFindings(lintFindings(t, files, withOptions(obj{"minLines": 9}))); !slices.Equal(got, want) {
		t.Errorf("minLines 9: got %v\nwant %v", got, want)
	}
	if got := lintFindings(t, files, withOptions(obj{"minLines": 9, "minTokens": 100})); len(got) != 0 {
		t.Errorf("minTokens 100: got %v", locatedFindings(got))
	}
}

func TestNoDuplicateCodeSkips(t *testing.T) {
	t.Parallel()
	const ignore = "// slop-police-ignore no-duplicate-code -- kept apart on purpose\n"
	imports := `import { a } from "./a.ts";
import { b } from "./b.ts";
import { c } from "./c.ts";
import { d } from "./d.ts";
import { e } from "./e.ts";
import { f } from "./f.ts";
import { g } from "./g.ts";
import { h } from "./h.ts";
import { i } from "./i.ts";
import { j } from "./j.ts";
import { k } from "./k.ts";
export { a, b, c, d, e, f, g, h, i, j, k };
`
	tests := map[string]map[string]string{
		// The ignore comment takes the code out, so neither copy is reported.
		"ignored copy":     {"a.ts": loadFunction("loadUsers"), "b.ts": ignore + loadFunction("loadPosts")},
		"ignored original": {"a.ts": ignore + loadFunction("loadUsers"), "b.ts": loadFunction("loadPosts")},
		// Test files are neither reported nor copied from.
		"test copy":     {"a.ts": loadFunction("loadUsers"), "b.test.ts": loadFunction("loadPosts")},
		"test original": {"a.test.ts": loadFunction("loadUsers"), "b.ts": loadFunction("loadPosts")},
		"imports":       importingFiles(imports),
	}
	for name, files := range tests {
		if got := lintFindings(t, files, duplicateCodeConfig); len(got) != 0 {
			t.Errorf("%s: got %v", name, locatedFindings(got))
		}
	}
}

func TestNoDuplicateCodeJsxIndentation(t *testing.T) {
	t.Parallel()
	card := `<section className="card">
  <h2 className="card-title">Pro plan</h2>
  <p className="card-text">
    Unlimited decks, cards and reviews,
    with priority support.
  </p>
  <ul className="card-list">
    <li>{t("pro_feature_decks")}</li>
    <li>{t("pro_feature_ai")}</li>
    <li>{t("pro_feature_support")}</li>
  </ul>
  <button onClick={onUpgrade}>{t("upgrade")}</button>
</section>`
	indent := func(text, prefix string) string { return strings.ReplaceAll(text, "\n", "\n"+prefix) }
	results := lintFindings(t, map[string]string{
		"a.tsx": "export const A = () => (\n  " + indent(card, "  ") + "\n);\n",
		"b.tsx": "export const B = () => (\n  <div>\n    " + indent(card, "    ") + "\n  </div>\n);\n",
	}, duplicateCodeConfig)
	want := []locatedFinding{{"b.tsx", 3, 5, "Lines 3-15 duplicate a.tsx:2-14"}}
	if got := locatedFindings(results); !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}
