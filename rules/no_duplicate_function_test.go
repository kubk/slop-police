package rules_test

import (
	"slices"
	"strings"
	"testing"
)

var duplicateFunctionConfig = obj{"rules": obj{"no-duplicate-function": "error"}}

// loadBody is a function body of three statements and well over 16 nodes.
func loadBody(local, callee, path string) string {
	return strings.NewReplacer("LOCAL", local, "CALLEE", callee, "PATH", path).Replace(`{
  const LOCAL = CALLEE("PATH" + id);
  if (!LOCAL.ok) {
    throw new Error("failed to load PATH");
  }
  return LOCAL.data;
}`)
}

func TestNoDuplicateFunction(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{
		"a.ts": "export function loadUsers(id: string) " + loadBody("response", "fetchJson", "/users/") + "\n\n" +
			"export function loadAdmins(id: string) " + loadBody("result", "fetchJson", "/admins/") + "\n",
		// Renamed locals and other literal values still match.
		"b.ts": strings.ReplaceAll("export const loadPosts = (id: number) => "+loadBody("res", "fetchJson", "/posts/"), "id", "postId") + ";\n",
		"c.ts": "export class Api {\n  loadTeams(id: string) " + loadBody("team", "fetchJson", "/teams/") + "\n}\n",
		// Silenced, but still a copy the others are reported against.
		"d.ts": "// slop-police-ignore no-duplicate-function\nexport function loadTags(id: string) " + loadBody("tags", "fetchJson", "/tags/") + "\n",
		// Different callee, async, or parameter count: different functions.
		"e.ts": "export function loadText(id: string) " + loadBody("response", "fetchText", "/text/") + "\n\n" +
			"export async function loadAsync(id: string) " + loadBody("response", "fetchJson", "/async/") + "\n\n" +
			"export function loadPage(id: string, page: number) " + loadBody("response", "fetchJson", "/pages/") + "\n",
	}, duplicateFunctionConfig)
	want := []locatedFinding{
		{"a.ts", 1, 17, "Function loadUsers has the same body shape as loadPosts (b.ts:1), loadTeams (c.ts:2), loadTags (d.ts:2) and 1 more"},
		{"a.ts", 9, 17, "Function loadAdmins has the same body shape as loadPosts (b.ts:1), loadTeams (c.ts:2), loadTags (d.ts:2) and 1 more"},
		{"b.ts", 1, 14, "Function loadPosts has the same body shape as loadUsers (a.ts:1), loadAdmins (a.ts:9), loadTeams (c.ts:2) and 1 more"},
		{"c.ts", 2, 3, "Method loadTeams has the same body shape as loadUsers (a.ts:1), loadAdmins (a.ts:9), loadPosts (b.ts:1) and 1 more"},
	}
	if got := locatedFindings(results); !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestNoDuplicateFunctionNeedsThreeFiles(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{
		"a.ts": "export function loadUsers(id: string) " + loadBody("response", "fetchJson", "/users/") + "\n\n" +
			"export function loadAdmins(id: string) " + loadBody("response", "fetchJson", "/admins/") + "\n",
		"b.ts": "export function loadPosts(id: string) " + loadBody("response", "fetchJson", "/posts/") + "\n",
		// Only differs in a name it doesn't declare.
		"c.ts": "export function loadText(id: string) " + loadBody("response", "fetchText", "/text/") + "\n",
		// Test files are neither reported nor counted.
		"d.test.ts": "export function loadMock(id: string) " + loadBody("response", "fetchJson", "/mock/") + "\n",
	}, duplicateFunctionConfig)
	if len(results) != 0 {
		t.Errorf("got %v", locatedFindings(results))
	}
}

func TestNoDuplicateFunctionMinFiles(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"a.ts": "export function loadUsers(id: string) " + loadBody("response", "fetchJson", "/users/") + "\n",
		"b.ts": "export function loadPosts(id: string) " + loadBody("response", "fetchJson", "/posts/") + "\n",
		"c.ts": "export function loadTeams(id: string) " + loadBody("response", "fetchJson", "/teams/") + "\n",
	}
	withMinFiles := func(minFiles float64) obj {
		return obj{"rules": obj{"no-duplicate-function": []any{"error", obj{"minFiles": minFiles}}}}
	}
	if got := len(lintFindings(t, files, withMinFiles(4))); got != 0 {
		t.Errorf("minFiles 4: got %d findings, want 0", got)
	}
	delete(files, "c.ts")
	want := []locatedFinding{
		{"a.ts", 1, 17, "Function loadUsers has the same body shape as loadPosts (b.ts:1)"},
		{"b.ts", 1, 17, "Function loadPosts has the same body shape as loadUsers (a.ts:1)"},
	}
	// Below 2 counts as 2: a shape in one file isn't a cross-file duplicate.
	for _, minFiles := range []float64{2, 1} {
		if got := locatedFindings(lintFindings(t, files, withMinFiles(minFiles))); !slices.Equal(got, want) {
			t.Errorf("minFiles %v: got %v\nwant %v", minFiles, got, want)
		}
	}
}

func TestNoDuplicateFunctionSkipsSmallFunctions(t *testing.T) {
	t.Parallel()
	small := map[string]string{
		// One statement, however large.
		"single": `(id: string) {
  return fetchJson("/users/" + id).then((response) => response.data ?? response.fallback ?? null);
}`,
		// Two statements, but fewer than 16 nodes.
		"tiny": `(id: string) {
  const user = load(id);
  return user.name;
}`,
		"passThrough": `(id: string, options: object) {
  return fetchJson(id, options);
}`,
	}
	for name, body := range small {
		results := lintFindings(t, map[string]string{
			"a.ts": "export function a" + body + "\n",
			"b.ts": "export function b" + body + "\n",
			"c.ts": "export function c" + body + "\n",
		}, duplicateFunctionConfig)
		if len(results) != 0 {
			t.Errorf("%s: got %v", name, locatedFindings(results))
		}
	}
}
