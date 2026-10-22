package rules_test

import (
	"strings"
	"testing"
)

var redundantAliasRules = obj{"rules": obj{"no-redundant-value-alias": "error"}}

func TestRedundantValueAliasFinding(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{"fixture.ts": `const CONTEXT_LENGTH = 60;
function search() {
  const contextLength = CONTEXT_LENGTH;
  let length = (CONTEXT_LENGTH);
  return contextLength + length;
}`}, redundantAliasRules)
	if len(results) != 2 {
		t.Fatalf("got %v, want 2 findings", results)
	}
	first := results[0]
	if first.Rule != "no-redundant-value-alias" || first.Line != 3 || !strings.Contains(first.Message, "contextLength only renames CONTEXT_LENGTH") {
		t.Errorf("unexpected first finding %+v", first)
	}
}

func TestRedundantValueAlias(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files map[string]string
		want  int
	}{
		{name: "snapshots, calls, property reads, typed aliases, destructuring and top-level aliases", want: 0, files: fixture(`
import { imported } from './other';
const VALUE = 1;
export const publicValue = VALUE;
const topLevel = VALUE;
namespace Public { export const exported = VALUE; }
function example(input) {
  let count = 1;
  const previous = count;
  count++;
  const parameter = input;
  const external = imported;
  const property = store.count;
  const result = calculate();
  const typed: number = VALUE;
  const { value } = store;
  const destructured = value;
}`)},
		{name: "shadowed binding inside a nested function", want: 1, files: fixture(`const VALUE = 1;
function example() {
  let alias = VALUE;
  function update(alias) { alias++; }
  return alias;
}`)},
		{name: "shadowed source binding", want: 0, files: fixture(`const VALUE = 1;
function example(VALUE) { const alias = VALUE; return alias; }`)},
		{name: "alias read inside a shadowing scope", want: 0, files: fixture(`const VALUE = 1;
function example() {
  const alias = VALUE;
  function nested(VALUE) { return alias; }
}`)},
		{name: "shorthand alias read inside a shadowing scope", want: 0, files: fixture(`const VALUE = 1;
function example() {
  const alias = VALUE;
  function nested(VALUE) { return { alias }; }
}`)},
		{name: "object mutation is not reassignment", want: 1, files: fixture(`const OBJECT = {};
function example() { const alias = OBJECT; alias.value = 2; }`)},
		{name: "writes to unrelated property names", want: 1, files: fixture(`const VALUE = 1;
function example() { let alias = VALUE; object.alias = 2; return { alias }; }`)},
		{name: "declaration files", want: 0, files: map[string]string{"fixture.d.ts": `declare const VALUE: number;
declare function example(): void;
declare namespace Values { const alias: typeof VALUE; }`}},
		{name: "null narrowing preserved in function declarations", want: 0, files: fixture(`function example(ref: { current: { close(): void } | null }) {
  const host = ref.current;
  if (!host) return;
  const mountedHost = host;
  function destroy() { mountedHost.close(); }
  return destroy;
}`)},
		{name: "discriminated union narrowing preserved", want: 0, files: fixture(`function example(input: { kind: "ready"; value: number } | { kind: "empty" }) {
  const state = input;
  if (state.kind !== "ready") return;
  const readyState = state;
  function read() { return readyState.value; }
  return read;
}`)},
		{name: "guarded alias inside a positive branch", want: 0, files: fixture(`function example(ref) {
  const host = ref.current;
  if (host) {
    const mountedHost = host;
    function destroy() { mountedHost.close(); }
    return destroy;
  }
}`)},
		{name: "alias after a null guard that throws", want: 0, files: fixture(`function example(ref) {
  const host = ref.current;
  if (host === null) { throw new Error(); }
  const mountedHost = host;
  function destroy() { mountedHost.close(); }
  return destroy;
}`)},
		{name: "condition without an early exit", want: 1, files: fixture(`function example(ref) {
  const host = ref.current;
  if (!host) { console.log("Missing"); }
  const mountedHost = host;
  function destroy() { mountedHost.close(); }
  return destroy;
}`)},
		{name: "unrelated guard", want: 1, files: fixture(`function example(ref, other) {
  const host = ref.current;
  if (!other) return;
  const mountedHost = host;
  function destroy() { mountedHost.close(); }
  return destroy;
}`)},
		{name: "captured alias whose source needs no narrowing", want: 1, files: fixture(`const CONTEXT_LENGTH = 60;
function example() {
  const contextLength = CONTEXT_LENGTH;
  function read() { return contextLength; }
  return read;
}`)},
		{name: "narrowed alias in an arrow function that keeps the guard", want: 1, files: fixture(`function example(ref: { current: { close(): void } | null }) {
  const host = ref.current;
  if (!host) return;
  const mountedHost = host;
  return () => mountedHost.close();
}`)},
		{name: "captured alias used with optional access", want: 1, files: fixture(`function example(ref: { current: { close(): void } | null }) {
  const host = ref.current;
  if (!host) return;
  const mountedHost = host;
  function destroy() { mountedHost?.close(); }
  return destroy;
}`)},
		{name: "captured alias used as a shorthand key", want: 1, files: fixture(`const VALUE = 1;
function example() {
  const alias = VALUE;
  function read(): { alias: number } { return { alias }; }
  return read;
}`)},
		{name: "compiler errors do not hide captured aliases", want: 1, files: fixture(`const VALUE = 1;
function example() {
  const alias = VALUE;
  const error: number = "text";
  function read() { return alias + error; }
  return read;
}`)},
		{name: "coordinate aliases", want: 2, files: fixture(`function draw(scale: number) {
  const topPadding = 18 * scale;
  const drawY = topPadding;
  const outerPadding = 20 * scale;
  const panelX = outerPadding;
  return [drawY, panelX];
}`)},
		{name: "alias passed in an object literal", want: 1, files: fixture(`const VALUE = 1;
function example() {
  const alias = VALUE;
  unknownCall({ value: alias });
}`)},
		{name: "alias of an exported constant declared locally", want: 1, files: fixture(`export const VALUE = 1;
export function example() {
  const alias = VALUE;
  return alias;
}`)},
		{name: "alias of an imported constant", want: 0, files: map[string]string{
			"constants.ts": "export const VALUE = 1;",
			"fixture.ts": `import { VALUE } from "./constants.ts";
export function example() {
  const alias = VALUE;
  return alias;
}`,
		}},
	}
	for _, write := range []string{
		"alias = 2",
		"alias += 1",
		"alias ||= 1",
		"alias++",
		"--alias",
		"[alias] = values",
		"({ value: alias } = object)",
		"({ alias } = object)",
		"[alias = 2] = values",
		"[...alias] = values",
		"({ ...alias } = object)",
		"for (alias of values) {}",
		"for (alias in object) {}",
		"function update() { alias = 2; }",
	} {
		tests = append(tests, struct {
			name  string
			files map[string]string
			want  int
		}{
			name:  "reassigned alias: " + write,
			files: fixture("const VALUE = 1; function example() { let alias = VALUE; " + write + "; }"),
		})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := lintFindings(t, test.files, redundantAliasRules); len(got) != test.want {
				t.Errorf("got %v, want %d findings", got, test.want)
			}
		})
	}
}

func fixture(source string) map[string]string {
	return map[string]string{"fixture.ts": source}
}
