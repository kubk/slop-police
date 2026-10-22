package rules_test

import (
	"slices"
	"testing"
)

func TestUnusedMethods(t *testing.T) {
	t.Parallel()
	hiddenMembers := map[string]string{"store.ts": `
export class Store {
  private hidden() {}
  protected shared() {}
}
`}
	tests := []struct {
		name    string
		files   map[string]string
		options obj
		want    []string
	}{
		{
			name: "method never referenced",
			files: map[string]string{"store.ts": `
class Store {
  used() {}
  unused() {}
  get total() { return 1; }
}

new Store().used();
`},
			want: []string{"Method Store.unused is never used", "Getter Store.total is never used"},
		},
		{
			name: "members implementing a library interface",
			files: map[string]string{
				// .d.ts files aren't analyzed, like a package in node_modules.
				"library.d.ts": `
export interface FileSystem {
  read(path: string): string;
  stat?(path: string): number;
}
export declare function mount(fs: FileSystem): void;
`,
				"fs.ts": `
import { mount, type FileSystem } from "./library";
class MemoryFileSystem implements FileSystem {
  read(path: string) { return path; }
  stat(path: string) { return path.length; }
  unused() {}
}
mount(new MemoryFileSystem());
`,
			},
			want: []string{"Method MemoryFileSystem.unused is never used"},
		},
		{
			name:  "private and protected skipped by default",
			files: hiddenMembers,
		},
		{
			name:    "private and protected when enabled",
			files:   hiddenMembers,
			options: obj{"includePrivate": true, "includeProtected": true},
			want:    []string{"Method Store.hidden is never used", "Method Store.shared is never used"},
		},
		{
			name: "members required by a structurally typed JSX prop",
			files: map[string]string{"fixture.tsx": `
class ProductionStore {
  close() {}
}

type ViewStore = Pick<ProductionStore, "close">;

function View({ store }: { store: ViewStore }) {
  store.close();
  return null;
}

class PlaygroundStore {
  close() {}
  actuallyUnused() {}
}

const store = new PlaygroundStore();
const view = <View store={store} />;
`},
			want: []string{"Method PlaygroundStore.actuallyUnused is never used"},
		},
		{
			name: "passing a full class type does not use every member",
			files: map[string]string{"fixture.tsx": `
class Store {
  unused() {}
}

function View({ store }: { store: Store }) {
  return store ? null : null;
}

const store = new Store();
const view = <View store={store} />;
`},
			want: []string{"Method Store.unused is never used"},
		},
		{
			name: "members named by Pick",
			files: map[string]string{"store.ts": `
class Store {
  picked() {}
  unused() {}
}

type StoreView = Pick<Store, "picked">;
`},
			want: []string{"Method Store.unused is never used"},
		},
		{
			name: "members used through an implemented interface",
			files: map[string]string{"platform.ts": `
interface Platform {
  open(): void;
  readonly title: string;
}

class BrowserPlatform implements Platform {
  open() {}
  get title() { return "browser"; }
  unused() {}
}

class TelegramPlatform implements Platform {
  open() {}
  get title() { return "telegram"; }
}

function run(platform: Platform) {
  platform.open();
  return platform.title;
}

run(Math.random() > 0.5 ? new BrowserPlatform() : new TelegramPlatform());
`},
			want: []string{"Method BrowserPlatform.unused is never used"},
		},
		{
			name: "calls from test files count",
			files: map[string]string{
				"store.ts": `
export class Store {
  reset() {}
}
`,
				"store.test.ts": `
import { Store } from "./store.ts";
new Store().reset();
`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := test.options
			if options == nil {
				options = obj{}
			}
			results := lintFindings(t, test.files, obj{"rules": obj{"unused-methods": []any{"error", options}}})
			if got := messages(results); !slices.Equal(got, test.want) {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}
