package rules_test

import (
	"slices"
	"testing"
)

func TestNoPlaceholderComment(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{
		"fixture.ts": `#!/usr/bin/env node
// Handle more cases
export function validate(input: string) {
  const url = "https://example.com/implement it here";
  const pattern = /\/\/ implement it here/;
  const template = ` + "`${url} // implement it here ${pattern}`" + `;
  if (input) { /* add more validation */ }
  return [url, pattern, template]; // can be extended in the future
  /**
   * This handler
   * is a future enhancement.
   */
}

export function empty() {
  // TODO: implement caching here
}

// slop-police-ignore no-placeholder-comment -- kept for the demo
// Customize this logic
export const config = {
  retries: 3, // extend this module
};

// Implement nothing; this comment is ordinary.
`,
		"view.tsx": `
export const View = () => (
  <div title="implement it here">
    implement it here // not a comment
    {/* implement the footer here */}
  </div>
);
`,
	}, obj{"rules": obj{"no-placeholder-comment": "error"}})
	want := []locatedFinding{
		{"fixture.ts", 2, 1, `Comment only promises future work: "Handle more cases"`},
		{"fixture.ts", 7, 16, `Comment only promises future work: "add more validation"`},
		{"fixture.ts", 8, 36, `Comment only promises future work: "can be extended in the future"`},
		{"fixture.ts", 9, 3, `Comment only promises future work: "future enhancement"`},
		{"fixture.ts", 16, 3, `Comment only promises future work: "implement caching here"`},
		{"fixture.ts", 22, 15, `Comment only promises future work: "extend this module"`},
		{"view.tsx", 5, 6, `Comment only promises future work: "implement the footer here"`},
	}
	if got := locatedFindings(results); !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}
