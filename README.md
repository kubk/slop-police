# slop-police

A fast, type-aware linter for TypeScript that catches the dead code and needless indirection AI coding agents tend to leave behind: methods nobody calls, object keys nobody reads, getters and aliases that only rename something.

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
    "no-single-string-cn": "off"
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

## TypeScript version

The binary embeds the TypeScript 7.0.2 compiler (typescript-go), so it doesn't use the `typescript` package from your project. Your tsconfigs need to work with TypeScript 7.

## Development

```sh
go test ./...
go build ./cmd/slop-police
```

The Go module path is `github.com/microsoft/typescript-go/x/slop-police`. That's on purpose: it lets the linter import typescript-go's internal packages (checker, find-all-references, project loading), which Go only allows from paths under the same prefix. `go.mod` pins typescript-go to the exact commit a TypeScript release was built from; to upgrade, bump that pin and fix what breaks.

### Releasing

Bump `version` in `npm/slop-police/package.json`, commit, then push a matching tag:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

CI builds the binaries for macOS, Linux and Windows (arm64 and x64) and publishes `slop-police` plus one `slop-police-<os>-<cpu>` package per platform, which npm installs as an optional dependency. Publishing needs an `NPM_TOKEN` repository secret.

## License

MIT. The published binaries include the TypeScript compiler and other dependencies; their licenses ship in `THIRD_PARTY_NOTICES.txt`.
