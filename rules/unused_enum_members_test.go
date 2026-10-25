package rules_test

import (
	"slices"
	"testing"
)

func TestUnusedEnumMembers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files map[string]string
		want  []finding
	}{
		{
			name: "members never read",
			files: map[string]string{
				"status.ts": `
export enum Status {
  Active,
  Archived = "archived",
  Deleted = Active + 1,
  Unused,
}
`,
				"view.ts": `
import { Status as S } from "./status.ts";
import * as status from "./status.ts";
export const isDeleted = (s: S) => s === S.Deleted;
export const archived: status.Status.Archived = status.Status["Archived"];
`,
			},
			want: []finding{{6, "Enum member Status.Unused is never used"}},
		},
		{
			name: "enums used as types still report their members",
			files: map[string]string{"color.ts": `
const enum Color { Red, Green }
export function paint(color: Color) { return color === Color.Red; }
`},
			want: []finding{{2, "Enum member Color.Green is never used"}},
		},
		{
			name: "enums used without naming members are skipped",
			files: map[string]string{
				"enums.ts": `
export enum Listed { A, B }
export enum Indexed { A, B }
export enum Keyed { A, B }
export enum Passed { A, B }
export enum Renamed { A, B }
export const all = Object.values(Listed);
export const byIndex = (i: number) => Indexed[i];
export type Key = keyof typeof Keyed;
console.log(Passed);
export { Renamed as Alias };
`,
			},
		},
		{
			name: "uses in tests count, enums in tests aren't checked",
			files: map[string]string{
				"mode.ts": "export enum Mode { Light, Dark }\n",
				"mode.test.ts": `
import { Mode } from "./mode.ts";
enum Local { Unused }
export const dark = Mode.Dark;
`,
			},
			want: []finding{{1, "Enum member Mode.Light is never used"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			results := lintFindings(t, tt.files, obj{"rules": obj{"unused-enum-members": "error"}})
			if got := lineMessages(results); !slices.Equal(got, tt.want) {
				t.Errorf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}
