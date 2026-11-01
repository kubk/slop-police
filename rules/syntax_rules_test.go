package rules_test

import (
	"slices"
	"testing"
)

func TestSyntaxRules(t *testing.T) {
	t.Parallel()
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
