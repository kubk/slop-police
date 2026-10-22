# slop-police

A fast, type-aware linter for TypeScript that catches the dead code and needless indirection AI coding agents tend to leave behind: methods nobody calls, object keys nobody reads, getters, aliases and wrappers that only rename or forward something, helpers copied instead of reused, comments that only promise future work, and errors swallowed or flattened into strings.

It runs the TypeScript 7 compiler in-process, so rules resolve symbols the same way `tsc` does, across all of your tsconfig projects at once.

```sh
npm i -D slop-police
npx slop-police
```

```
packages/frontend/src/store/deck-store.ts:42:3  error  unused-methods      Method DeckStore.reset is never used
packages/api/src/shared/links/links.ts:30:3     error  unused-object-keys  links.privacyPolicy is never read

2 problems (2 errors, 0 warnings)
```

## Configuration

slop-police reads `slop-police.json` from the current directory:

```json
{
  "projects": ["packages/api/tsconfig.json", "packages/frontend/tsconfig.app.json"],
  "ignore": ["**/.next/**"],
  "testFiles": ["**/*.test.{ts,tsx}", "**/*.spec.{ts,tsx}"],
  "rules": {
    "unused-methods": ["error", { "includePrivate": true }],
    "unused-object-keys": "error",
    "no-identity-type-alias": "warn",
    "no-pass-through-getter": "error",
    "no-redundant-value-alias": "error",
    "no-single-string-cn": "off",
    "no-pass-through-wrapper": "error",
    "no-duplicate-function": "warn",
    "no-placeholder-comment": "error",
    "no-promise-default-fallback": "error",
    "no-stringified-unknown-error": "warn"
  }
}
```

- `projects`: tsconfig files to analyze. Usages are counted across all of them, so a method used only by another package still counts as used.
- `ignore`: glob patterns (relative to the config) for files to skip entirely.
- `testFiles`: glob patterns for tests. Usages inside tests count, but declarations inside tests are not reported.
- `rules`: `"error"`, `"warn"` or `"off"`, or `[severity, options]`. Rules that aren't listed are off.

Exit code is `1` when there are errors, `2` for an invalid config, `0` otherwise.

## Rules

### unused-methods

Class methods, getters and setters that are never referenced anywhere in the analyzed projects.

```ts
class Store {
  load() {}
  reset() {} // Method Store.reset is never used
}
new Store().load();
```

A member also counts as used when it implements an interface member that is used, when it's named in `Pick<Class, "name">`, or when an instance is passed to a JSX prop whose type requires it. Options: `includePrivate` and `includeProtected` (both `false` by default).

### unused-object-keys

Keys of a top-level `const` object literal that are never read, as long as every use of the object reads statically known keys (`links.a`, `links["a"]`, `const { a } = links`). Objects that escape (passed to a function, spread, accessed dynamically) are skipped.

```ts
export const links = {
  docs: "https://example.com/docs",
  legacy: "https://example.com/old", // links.legacy is never read
};
open(links.docs);
```

### no-identity-type-alias

A type alias that only renames another type.

```ts
type User = { id: string };
type Member = User; // Type alias Member only renames User
```

### no-pass-through-getter

A getter that only returns another property of `this`.

```ts
class Store {
  private items: Item[] = [];
  get list() { return this.items; } // Getter list only returns this.items
}
```

### no-pass-through-wrapper

A function or method whose body only returns a call (awaited or not) to a named function, passing its own parameters on as they are.

```ts
function loadUser(id: string) { // Function loadUser only forwards to api.loadUser
  return api.loadUser(id);
}
```

It uses types to skip wrappers that do something:

- the shape is required: a method of a typed object literal (a `Proxy` handler, a Worker's `fetch`), a method that implements or overrides an inherited member, or a function assigned to a variable with a declared type. An override that only calls `super`'s method of the same name is still reported.
- the wrapper adds types: type parameters, overloads, or parameter and return types declared differently from the callee's, like `t(key: TranslationKey)` around `translate(key: string)`.
- it delegates to a private or protected member, like `get(key) { return this.items.get(key); }`.

Calls on computed values (`getDay(date).getTime()`, `/^\s*$/.test(text)`) aren't forwarding, so they don't count.

### no-redundant-value-alias

A local `const` that only renames another `const`, when using the original directly means the same thing.

```ts
function render() {
  const user = getUser();
  const current = user; // Local alias current only renames user; use the original binding directly
  return current.name;
}
```

### no-single-string-cn

`cn()` called with a single static class string.

```tsx
<div className={cn("flex gap-2")} /> // Use the class string directly instead of wrapping it in cn()
```

### no-placeholder-comment

A comment that only promises future work instead of describing the code: "add more validation", "handle more cases", "can be extended in the future", "extend this logic", "customize this behavior", "future enhancement", "implement … here". Ordinary TODOs don't match.

```ts
function parse(input: string) {
  // Add more validation here
  return JSON.parse(input);
}
```

### no-duplicate-function

A function whose body has the same shape as functions in at least two other files: a helper that was rewritten instead of reused. Shapes ignore the names of parameters and locals and the values of literals, but not the names of what the body calls or reads. Bodies with fewer than 2 statements or 16 syntax nodes, and functions in test files, don't count. Option: `minFiles` (default `3`, at least `2`), the number of files a shape has to appear in.

```ts
// users.ts, posts.ts and teams.ts each have:
export async function loadUsers(id: string) {
  const response = await fetchJson("/users/" + id);
  if (!response.ok) throw new Error("failed to load users");
  return response.data;
}
// Function loadUsers has the same body shape as loadPosts (posts.ts:1), loadTeams (teams.ts:1)
```

### no-promise-default-fallback

A promise `.catch()` handler that hides the rejection: an empty handler, or one that only returns `null`, `undefined`, `false`, `0`, `""`, `[]` or `{}`, optionally after `console.*` or `logger.*` calls. Only `.catch` on promises and other thenables counts.

```ts
const config = await fetchConfig().catch(() => null); // Promise rejection is replaced with null
navigator.clipboard.writeText(text).catch(() => {}); // Promise rejection is swallowed by an empty catch handler
const enabled = await fetchFlag().catch((error) => { // Promise rejection is logged and replaced with false
  console.error("flag load failed", error);
  return false;
});
```

### no-stringified-unknown-error

`error instanceof Error ? error.message : String(error)` (in either order), which flattens a caught value into a string and drops its type, stack and cause. A plain `instanceof Error` check counts when the variable is named `error`, `err`, `e` or `cause`; an `||` of `instanceof` checks counts for any name.

```ts
try {
  await sendMessage(chatId, text);
} catch (error) {
  return { ok: false, error: error instanceof Error ? error.message : String(error) }; // Caught value error is flattened into a string
}
```

## Ignoring findings

A `// slop-police-ignore` comment directly above a statement, class member or object property silences findings in it, including everything nested inside. List rule names to silence only those, and add a reason after `--`:

```ts
class Store {
  // slop-police-ignore unused-methods -- called from the devtools console
  debug() {}
}

// slop-police-ignore
export const legacyLinks = { a: "/a", b: "/b" };
```

For `no-placeholder-comment`, put the ignore comment on the line directly above the reported comment.

## Requirements

macOS or Linux on arm64 or x64 (on Windows, use WSL). The linter bundles its own TypeScript 7 compiler, so your tsconfigs need to work with TypeScript 7.

## License

MIT
