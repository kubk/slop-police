package rules_test

import (
	"slices"
	"testing"
)

func TestUnusedObjectKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "keys never read by name",
			files: map[string]string{
				"styles.ts": `
export const styles = {
  used: "a",
  unused: "b",
};
`,
				"view.ts": `
import { styles } from "./styles.ts";
export const className = styles.used;
`,
			},
			want: []string{"styles.unused is never read"},
		},
		{
			name: "literal element access, destructuring and namespace imports",
			files: map[string]string{
				"styles.ts": `
export const styles = {
  a: "a",
  b: "b",
  c: "c",
  d: "d",
  unused: "e",
};
`,
				"view.ts": `
import * as s from "./styles.ts";
import { styles } from "./styles.ts";
const { b } = styles;
export const all = [styles["a"], b, s.styles.c, styles?.d];
`,
			},
			want: []string{"styles.unused is never read"},
		},
		{
			name: "non-static use reads every key",
			files: map[string]string{"objects.ts": `
type Outcome = "a" | "b";
export const dynamic = { a: 1, b: 2 };
export const spread = { a: 1, b: 2 };
export const passed = { a: 1, b: 2 };
export const typed = { a: 1, b: 2 };
export const keysRead = { a: 1, b: 2 };
export const shorthand = { a: 1, b: 2 };
export const read = (outcome: Outcome) => dynamic[outcome];
export const copy = { ...spread };
export const result = JSON.stringify(passed);
export type Key = keyof typeof typed;
export const keys = Object.keys(keysRead);
export const wrapped = { shorthand };
`},
		},
		{
			name: "typed objects and objects using this",
			files: map[string]string{"objects.ts": `
type Labels = { a: string; b: string };
export const annotated: Labels = { a: "a", b: "b" };
export const satisfied = { a: "a", b: "b" } satisfies Labels;
export const store = {
  a() {
    return this.b();
  },
  b() {
    return 1;
  },
};
export const all = [annotated.a, satisfied.a, store.a()];
`},
		},
		{
			name: "reads in test files count, objects declared there are skipped",
			files: map[string]string{
				"cache.ts": `
export const cache = {
  get() {},
  clear() {},
};
cache.get();
`,
				"cache.test.ts": `
import { cache } from "./cache.ts";
const fixture = { used: 1, unused: 2 };
cache.clear();
fixture.used;
`,
			},
		},
		{
			name:  "objects never referenced",
			files: map[string]string{"objects.ts": "export const unreferenced = { a: 1 };"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			results := lintFindings(t, test.files, obj{"rules": obj{"unused-object-keys": "error"}})
			if got := messages(results); !slices.Equal(got, test.want) {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}
