package rules_test

import (
	"slices"
	"testing"
)

func TestUnusedExports(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		files   map[string]string
		options obj
		want    []locatedFinding
	}{
		{
			name: "exports used only in their file or never",
			files: map[string]string{
				"main.ts": `
import { used, type Shape } from "./lib.ts";
export const fromEntry = used({ size: 1 } satisfies Shape);
`,
				"lib.ts": `
export type Shape = { size: number };
export interface Options { shape: Shape }
export function used(shape: Shape, options?: Options) { return shape.size + (options ? LIMIT : 0); }
export const LIMIT = 10;
export function never() {}
export class Store {}
`,
			},
			want: []locatedFinding{
				{"lib.ts", 3, 18, "Export Options is only used in its own file"},
				{"lib.ts", 5, 14, "Export LIMIT is only used in its own file"},
				{"lib.ts", 6, 17, "Export never is never used"},
				{"lib.ts", 7, 14, "Export Store is never used"},
			},
		},
		{
			name: "renamed, default and type imports, JSX and shorthand properties",
			files: map[string]string{
				"main.tsx": `
import Button, { label as text, size } from "./button.tsx";
import type { Props } from "./button.tsx";
const props: Props = { size };
export const view = <Button {...props}>{text}</Button>;
`,
				"button.tsx": `
export type Props = { size: number };
export const label = "a";
export const size = 1;
export default function Button(props: Props & { children: string }) { return props.children; }
`,
			},
		},
		{
			name: "local export lists, recursion and default exports",
			files: map[string]string{
				"main.ts": `
import { a, walk } from "./lib.ts";
export const all = [a, walk];
`,
				"lib.ts": `
const a = 1;
const b = 2;
const c = b + 1;
export { a, b, c as renamed };
export function walk(n: number): number { return n > 0 ? walk(n - 1) : 0; }
export function recurse(n: number): number { return n > 0 ? recurse(n - 1) : 0; }
export default c;
`,
			},
			want: []locatedFinding{
				{"lib.ts", 5, 13, "Export b is only used in its own file"},
				{"lib.ts", 5, 21, "Export renamed is only used in its own file"},
				{"lib.ts", 7, 17, "Export recurse is never used"},
				{"lib.ts", 8, 1, "Default export is never used"},
			},
		},
		{
			name: "barrels pass uses through and report their own unused re-exports",
			files: map[string]string{
				"main.ts": `
import { a, viaStar } from "./lib/index.ts";
export const all = [a, viaStar];
`,
				"lib/index.ts": `
export { a, b } from "./values.ts";
export * from "./star.ts";
`,
				"lib/values.ts": `
export const a = 1;
export const b = 2;
`,
				"lib/star.ts": `
export const viaStar = 1;
export const unusedViaStar = 2;
`,
			},
			want: []locatedFinding{
				{"lib/index.ts", 2, 13, "Export b is never used"},
				{"lib/star.ts", 3, 14, "Export unusedViaStar is never used"},
			},
		},
		{
			name: "entry points expose what they re-export",
			files: map[string]string{
				"index.ts": `
export * from "./star.ts";
export * as ns from "./namespace.ts";
export { named } from "./named.ts";
`,
				"star.ts":      "export const a = 1;\nexport const b = 2;\n",
				"namespace.ts": "export const c = 1;\n",
				"named.ts":     "export const named = 1;\nexport const notExported = 2;\n",
			},
			want: []locatedFinding{
				{"named.ts", 2, 14, "Export notExported is never used"},
			},
		},
		{
			name: "namespace imports use members, or everything when the namespace escapes",
			files: map[string]string{
				"main.ts": `
import * as members from "./members.ts";
import * as escaped from "./escaped.ts";
import type * as types from "./types.ts";
export const used: types.Used = members.used;
export const all = Object.keys(escaped);
`,
				"members.ts": "export const used = 1;\nexport const unused = 2;\n",
				"escaped.ts": "export const a = 1;\nexport const b = 2;\n",
				"types.ts":   "export type Used = number;\nexport type Unused = string;\n",
			},
			want: []locatedFinding{
				{"members.ts", 2, 14, "Export unused is never used"},
				{"types.ts", 2, 13, "Export Unused is never used"},
			},
		},
		{
			name: "dynamic imports and import types use the whole module",
			files: map[string]string{
				"main.ts": `
export const page = import("./page.ts");
export type Config = typeof import("./config.ts");
export type Member = import("./member.ts").Used;
`,
				"page.ts":   "export default function Page() {}\nexport const loader = 1;\n",
				"config.ts": "export const a = 1;\n",
				"member.ts": "export type Used = 1;\nexport type Unused = 2;\n",
			},
			want: []locatedFinding{
				{"member.ts", 2, 13, "Export Unused is never used"},
			},
		},
		{
			name: "uses in tests count, exports of tests aren't checked",
			files: map[string]string{
				"lib.ts": "export const tested = 1;\nexport const untested = 2;\n",
				"lib.test.ts": `
import { tested } from "./lib.ts";
export const helper = tested;
`,
			},
			want: []locatedFinding{
				{"lib.ts", 2, 14, "Export untested is never used"},
			},
		},
		{
			name: "overloads, merged declarations, destructuring and ignore comments",
			files: map[string]string{
				"main.ts": `import "./lib.ts";`,
				"lib.ts": `
export function parse(input: string): string;
export function parse(input: number): number;
export function parse(input: string | number) { return input; }
export const Mode = { a: 1 };
export type Mode = keyof typeof Mode;
export const { x, y: [z] } = { x: 1, y: [2] };
// slop-police-ignore unused-exports -- read by the devtools console
export const debug = 1;
`,
			},
			want: []locatedFinding{
				{"lib.ts", 2, 17, "Export parse is never used"},
				{"lib.ts", 5, 14, "Export Mode is never used"},
				{"lib.ts", 7, 16, "Export x is never used"},
				{"lib.ts", 7, 23, "Export z is never used"},
			},
		},
		{
			name:    "with entry points, unreachable files are reported and their uses don't count",
			options: obj{"entry": []string{"src/main.ts"}},
			files: map[string]string{
				"src/main.ts": `
import { live } from "./live.ts";
export const app = live;
`,
				"src/live.ts": `
export const live = 1;
export const usedByDeadCode = 2;
`,
				"src/dead.ts": `
import { usedByDeadCode } from "./live.ts";
import { helper } from "./helper.ts";
export const dead = usedByDeadCode + helper;
`,
				"src/helper.ts": "export const helper = 1;\n",
				"src/global.ts": "declare global { var flag: boolean }\nexport {};\n",
				"src/script.ts": "const notAModule = 1;\n",
				"src/live.test.ts": `
import { live } from "./live.ts";
import { fixture } from "./fixture.ts";
export const check = [live, fixture];
`,
				"src/fixture.ts": "export const fixture = 1;\n",
			},
			want: []locatedFinding{
				{"src/dead.ts", 1, 1, "File is never imported from an entry point"},
				{"src/helper.ts", 1, 1, "File is never imported from an entry point"},
				{"src/live.ts", 3, 14, "Export usedByDeadCode is never used"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rule any = "error"
			if tt.options != nil {
				rule = []any{"error", tt.options}
			}
			results := lintFindings(t, tt.files, obj{"rules": obj{"unused-exports": rule}})
			if got := locatedFindings(results); !slices.Equal(got, tt.want) {
				t.Errorf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}
