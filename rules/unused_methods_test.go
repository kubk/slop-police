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
			name: "members overriding a library base class",
			files: map[string]string{
				"library.d.ts": `
export declare class Stream {
  write(chunk: string): void;
  protected _write(chunk: string): void;
  static from(chunks: string[]): Stream;
}
`,
				"stream.ts": `
import { Stream } from "./library";
class Upper extends Stream {
  _write(chunk: string) { console.log(chunk.toUpperCase()); }
  static from(chunks: string[]) { return new Upper(); }
  from() {}
  unused() {}
}
new Upper().write("a");
`,
			},
			want: []string{"Method Upper.from is never used", "Method Upper.unused is never used"},
		},
		{
			name: "static members read through a library's constructor type",
			files: map[string]string{
				"react.d.ts": `
declare namespace React {
  class Component<P, S> {
    props: P;
    state: S;
    render(): unknown;
  }
  class PureComponent<P, S> extends Component<P, S> {}
  interface StaticLifecycle<P, S> {
    getDerivedStateFromProps?: (props: P, state: S) => Partial<S> | null;
  }
  interface ComponentClass<P, S> extends StaticLifecycle<P, S> {
    new (props: P): Component<P, S>;
    displayName?: string;
  }
}
export = React;
`,
				"counter.ts": `
import { Component, PureComponent } from "./react";
abstract class Base<P, S> extends Component<P, S> {}
class Counter extends PureComponent<{ start: number }, { count: number }> {
  static getDerivedStateFromProps(props: { start: number }) { return { count: props.start }; }
  static get displayName() { return "Counter"; }
  static unused() {}
  render() { return this.state.count; }
}
class Label extends Base<{ text: string }, {}> {
  static getDerivedStateFromProps() { return null; }
  getDerivedStateFromProps() { return null; }
  render() { return this.props.text; }
}
console.log(Counter, Label);
`,
			},
			want: []string{"Method Counter.unused is never used", "Method Label.getDerivedStateFromProps is never used"},
		},
		{
			name: "members used through an analyzed base class",
			files: map[string]string{"shapes.ts": `
abstract class Shape {
  abstract area(): number;
  describe() { return "shape"; }
}

class Square extends Shape {
  area() { return 4; }
  describe() { return "square"; }
}

function measure(shape: Shape) {
  return shape.area();
}

measure(new Square());
`},
			want: []string{"Method Shape.describe is never used", "Method Square.describe is never used"},
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
