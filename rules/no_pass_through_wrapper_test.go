package rules_test

import (
	"slices"
	"testing"
)

func TestNoPassThroughWrapper(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{
		"fixture.ts": `declare const api: { loadUser(id: string, ...rest: unknown[]): Promise<unknown>; reset(): void };
declare const prisma: { user: { find(id: string): unknown } };
declare function fetchJson(url: string): Promise<unknown>;

export function loadUser(id: string) {
  return api.loadUser(id);
}

export async function loadUserAwaited(id: string, options: unknown) {
  return await api.loadUser(id, options);
}

export class Store {
  reset() {
    return api.reset();
  }
}

export const handlers = {
  load(id: string) {
    return api.loadUser(id);
  },
};

export const loadArrow = (id: string) => {
  return api.loadUser(id);
};

export const loadExpression = function (id: string) {
  return api.loadUser(id);
};

export const loadInline = (id: string) => api.loadUser(id);

export function loadWithLiteral(id: string) {
  return api.loadUser(id, "full");
}

export function loadWithProperty(user: { id: string }) {
  return api.loadUser(user.id);
}

export function loadWithSpread(...ids: string[]) {
  return api.loadUser(...ids);
}

export function loadDestructured({ id }: { id: string }) {
  return api.loadUser(id);
}

export function loadAndLog(id: string) {
  console.log(id);
  return api.loadUser(id);
}

// Wrapping an API or adding an alias comment doesn't make a wrapper useful.
export function getJson(url: string) {
  return fetchJson(url);
}

export function findUser(id: string) {
  return prisma.user.find(id);
}

// Alias kept for backwards compatibility
export function getUser(id: string) {
  return api.loadUser(id);
}

// slop-police-ignore no-pass-through-wrapper -- the public name of loadUser
export function readUser(id: string) {
  return api.loadUser(id);
}
`,
		"chains.ts": `declare function getDayStart(date: Date): Date;
declare const db: { select(): { from(table: string): { limit(n: number): unknown } } };

export function dayStartTime(date: Date) {
  return getDayStart(date).getTime();
}

export function firstRows(limit: number) {
  return db.select().from("rows").limit(limit);
}

export function isBlank(text: string) {
  return /^\s*$/.test(text);
}

export function format(value: number) {
  return new Intl.NumberFormat("en-US").format(value);
}

export function element(id: string) {
  return document["getElementById"](id);
}
`,
		"shapes.ts": `declare function handle(request: Request): Promise<Response>;

export const worker: { fetch(request: Request): Promise<Response> } = {
  fetch(request) {
    return handle(request);
  },
};

export const traced = new Proxy({} as Record<string, unknown>, {
  get(target, property, receiver) {
    return Reflect.get(target, property, receiver);
  },
});

export const typedHandler: (request: Request) => Promise<Response> = (request) => {
  return handle(request);
};

interface Handler {
  handle(request: Request): Promise<Response>;
}

export class RequestHandler implements Handler {
  handle(request: Request) {
    return handle(request);
  }
}

class Base {
  close() {}
}

export class Child extends Base {
  close() {
    return this.destroy();
  }
  destroy() {}
}

export class Overridden extends Base {
  override close() {
    return this.destroy();
  }
  destroy() {}
}

export class Redundant extends Base {
  close() {
    return super.close();
  }
}

export class Plain {
  close() {
    return this.destroy();
  }
  destroy() {}
}
`,
		"types.ts": `type Key = "title" | "body";
declare function translate(key: string, ...args: unknown[]): string;
declare function save(value: unknown): void;
declare function load(id: string): Promise<{ id: string; name: string }>;

export function t(key: Key) {
  return translate(key);
}

export function tAny(key: string) {
  return translate(key);
}

export function tUntyped(key) {
  return translate(key);
}

export function overloaded(key: Key): string;
export function overloaded(key: string): string;
export function overloaded(key: string) {
  return translate(key);
}

export function generic<T>(value: T) {
  return save(value);
}

declare const keys: unknown[];
export function isKey(value: unknown): value is Key {
  return keys.includes(value);
}

export function loadAny(id: any): any {
  return load(id);
}

export function loadName(id: string): Promise<{ name: string }> {
  return load(id);
}

export function loadSame(id: string): Promise<{ id: string; name: string }> {
  return load(id);
}
`,
		"members.ts": `class Items {
  get(key: string) { return key; }
}

export class Store {
  private items = new Items();
  protected guarded = new Items();
  #hidden = new Items();
  shared = new Items();

  constructor(private readonly injected: Items) {}

  getItem(key: string) {
    return this.items.get(key);
  }
  getGuarded(key: string) {
    return this.guarded.get(key);
  }
  getHidden(key: string) {
    return this.#hidden.get(key);
  }
  getInjected(key: string) {
    return this.injected.get(key);
  }
  getShared(key: string) {
    return this.shared.get(key);
  }
  load(key: string) {
    return this.loadImpl(key);
  }
  private loadImpl(key: string) {
    return key;
  }
}
`,
	}, obj{"rules": obj{"no-pass-through-wrapper": "error"}})
	want := []locatedFinding{
		{"chains.ts", 20, 17, `Function element only forwards to document["getElementById"]`},
		{"fixture.ts", 5, 17, "Function loadUser only forwards to api.loadUser"},
		{"fixture.ts", 9, 23, "Function loadUserAwaited only forwards to api.loadUser"},
		{"fixture.ts", 14, 3, "Method reset only forwards to api.reset"},
		{"fixture.ts", 20, 3, "Method load only forwards to api.loadUser"},
		{"fixture.ts", 25, 14, "Function loadArrow only forwards to api.loadUser"},
		{"fixture.ts", 29, 14, "Function loadExpression only forwards to api.loadUser"},
		{"fixture.ts", 57, 17, "Function getJson only forwards to fetchJson"},
		{"fixture.ts", 61, 17, "Function findUser only forwards to prisma.user.find"},
		{"fixture.ts", 66, 17, "Function getUser only forwards to api.loadUser"},
		{"members.ts", 25, 3, "Method getShared only forwards to this.shared.get"},
		{"members.ts", 28, 3, "Method load only forwards to this.loadImpl"},
		{"shapes.ts", 48, 3, "Method close only forwards to super.close"},
		{"shapes.ts", 54, 3, "Method close only forwards to this.destroy"},
		{"types.ts", 10, 17, "Function tAny only forwards to translate"},
		{"types.ts", 14, 17, "Function tUntyped only forwards to translate"},
		{"types.ts", 33, 17, "Function loadAny only forwards to load"},
		{"types.ts", 41, 17, "Function loadSame only forwards to load"},
	}
	if got := locatedFindings(results); !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}
