package rules_test

import (
	"slices"
	"testing"
)

func TestSyntaxRules(t *testing.T) {
	t.Parallel()
	const cnMessage = "Use the class string directly instead of wrapping it in cn()"
	tests := []struct {
		name   string
		rule   string
		source string
		want   []finding
	}{
		{
			name: "pass-through getter",
			rule: "no-pass-through-getter",
			source: `
class Store {
  private transition = "animated";

  get currentTransition() {
    return this.transition;
  }
}
`,
			want: []finding{{5, "Getter currentTransition only returns this.transition"}},
		},
		{
			name: "getters that derive a value or do more work",
			rule: "no-pass-through-getter",
			source: `
class Store {
  private transition = "animated";
  private state = { transition: "animated" };

  get isAnimated() {
    return this.transition === "animated";
  }

  get nestedTransition() {
    return this.state.transition;
  }

  get trackedTransition() {
    this.trackRead();
    return this.transition;
  }

  private trackRead() {}
}
`,
		},
		{
			name: "identity type aliases",
			rule: "no-identity-type-alias",
			source: `
type User = { id: string };
type Member = (User);
type Users = Array<User>;
type Id = User["id"];
`,
			want: []finding{{3, "Type alias Member only renames User"}},
		},
		{
			name: "cn with a single static string",
			rule: "no-single-string-cn",
			source: `
declare function cn(...classes: unknown[]): string;
declare const active: boolean;
export const a = cn("p-2");
export const b = cn(` + "`p-2`" + `);
export const c = cn("p-2", active && "font-bold");
export const d = cn(active ? "p-2" : "p-4");
`,
			want: []finding{{4, cnMessage}, {5, cnMessage}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			results := lintFindings(t, map[string]string{"fixture.ts": test.source}, obj{"rules": obj{test.rule: "error"}})
			if got := lineMessages(results); !slices.Equal(got, test.want) {
				t.Errorf("got %v, want %v", got, test.want)
			}
		})
	}
}
