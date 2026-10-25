<h1 align="center">slop-police</h1>

<p align="center"><b>Stop LLM crimes in your codebase</b></p>

<p align="center">
A type-aware TypeScript linter for the dead code and needless indirection AI agents leave behind.
</p>

```sh
pnpm add slop-police
# or
npm i slop-police
```

Run it with `pnpm exec slop-police` or `npx slop-police`.

Example output:

```
deck-store.ts:42:3  error  unused-methods      Method DeckStore.reset is never used
links.ts:30:3       error  unused-object-keys  links.privacyPolicy is never read

2 errors, 0 warnings
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
    "no-identity-type-alias": "warn",
    "no-single-string-cn": "off"
  }
}
```

- `projects`: tsconfig files to analyze. Usages are counted across all of them, so a method used only by another package still counts as used.
- `ignore`: glob patterns (relative to the config) for files to skip entirely. Files that `.gitignore` ignores are skipped too.
- `testFiles`: glob patterns for tests. Usages inside tests count, but declarations inside tests are not reported.
- `rules`: every built-in rule runs as an error with its default options. List a rule here to change that: `"error"`, `"warn"` or `"off"`, or `[severity, options]`. Plugin rules run only when they're listed.
- `plugins`: modules with [custom rules](#custom-rules), as paths relative to the config or package names.

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

A member also counts as used when it implements an interface member that is used or that is declared outside the analyzed code (a library calls it), when it's named in `Pick<Class, "name">`, or when an instance is passed to a JSX prop whose type requires it. Options: `includePrivate` and `includeProtected` (both `false` by default).

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

### no-duplicate-code

Copy-pasted code: at least 50 tokens in a row that repeat earlier code (in the same file or one before it by path) and span at least 11 lines. Tokens are compared by their text, types included, so a renamed variable or a different type ends a copy, but comments, formatting and imports are left out. Test files are neither reported nor compared. `// slop-police-ignore no-duplicate-code` above a statement, class member or object property takes it out of the comparison, so it is not reported as a copy or as the original. Options: `minLines` (default `11`, counting every line a copy spans) and `minTokens` (default `50`).

```ts
// update-card.ts and delete-card.ts both check the card the same way:
const userId = ctx.userId;
assert(userId);

const cards = await getCardsWithDeck(ctx.db, [input.id]);
if (cards.length === 0) {
  throw notFound("Card not found");
}
const card = cards[0];
if (card.deckAuthorId !== userId) {
  throw forbidden("You don't own this card");
}
// delete-card.ts:6:5  Lines 6-16 duplicate update-card.ts:6-16
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

### no-stringified-unknown-error

`error instanceof Error ? error.message : String(error)` (in either order), which flattens a caught value into a string and drops its type, stack and cause. A plain `instanceof Error` check counts when the variable is named `error`, `err`, `e` or `cause`; an `||` of `instanceof` checks counts for any name. A function that does nothing but this conversion, like `getErrorMessage(error)`, is where it belongs and isn't reported; use it instead of repeating the expression.

```ts
try {
  await sendMessage(chatId, text);
} catch (error) {
  return { ok: false, error: error instanceof Error ? error.message : String(error) }; // Caught value error is flattened into a string
}
```

## Custom rules

Write rules in TypeScript or JavaScript, list their modules in `plugins` and turn them on in `rules`:

```json
{
  "projects": ["tsconfig.json"],
  "plugins": ["./slop-rules.ts"],
  "rules": {
    "no-direct-fetch": ["error", { "allowIn": "src/api/" }],
    "no-needless-nullish-fallback": "warn"
  }
}
```

A plugin exports rules made with `defineRule`, by default or by name. `visit` has a handler per node kind, called for every node of that kind in every linted file:

```ts
// slop-rules.ts
import { defineRule, isIdentifier, SyntaxKind } from "slop-police";

export const noDirectFetch = defineRule({
  name: "no-direct-fetch",
  // Option defaults. The config's options must have the same types.
  options: { allowIn: "" },
  visit: {
    CallExpression(node, ctx) {
      if (ctx.options.allowIn && ctx.file.path.startsWith(ctx.options.allowIn)) return;
      if (isIdentifier(node.expression) && node.expression.text === "fetch") {
        ctx.report(node.expression, "Use the API client instead of fetch()");
      }
    },
  },
});

export const noNeedlessNullishFallback = defineRule({
  name: "no-needless-nullish-fallback",
  visit: {
    BinaryExpression(node, ctx) {
      if (node.operatorToken.kind !== SyntaxKind.QuestionQuestionToken) return;
      const { checker } = ctx;
      const type = checker.getTypeAtLocation(node.left);
      if (!type) return;
      const nullable = [checker.getUndefinedType(), checker.getNullType()].some((t) => checker.isTypeAssignableTo(t, type));
      if (!nullable) ctx.report(node.right, `${node.left.getText()} is never null or undefined`);
    },
  },
});
```

Nodes, `SyntaxKind`, the `is*` guards and the checker come from the TypeScript 7 API that slop-police runs on. The handler's `ctx` has:

- `options`: the defaults merged with the config's options.
- `file`: the file being visited: `path` (relative to the config), `fileName`, `isTest` and `sourceFile`.
- `checker` and `program`: those of the file's project. Each checker call is a round trip to the linter process, so check the syntax first.
- `report(node, message)`: reports a finding at the node's start. `// slop-police-ignore` comments work as for built-in rules.

For rules that compare files, `create(ctx)` returns the visitor instead, so its state lives in a closure, and `finish` runs after the last file:

```ts
import { defineRule, type Node } from "slop-police";

export const noRepeatedUrl = defineRule({
  name: "no-repeated-url",
  create(ctx) {
    const byUrl = new Map<string, Node[]>();
    return {
      StringLiteral(node) {
        if (node.text.startsWith("https://")) byUrl.set(node.text, [...(byUrl.get(node.text) ?? []), node]);
      },
      finish() {
        for (const [url, nodes] of byUrl) {
          if (nodes.length > 2) for (const node of nodes) ctx.report(node, `${url} is hard-coded in ${nodes.length} places`);
        }
      },
    };
  },
});
```

Test rules with `lint` from `slop-police/testing`, which runs a rule alone on a source string (as `index.ts`) or on files (an object of paths to contents) in a strict TypeScript project:

```ts
import { lint } from "slop-police/testing";
import { noDirectFetch } from "./slop-rules.ts";

test("no-direct-fetch", () => {
  expect(lint(noDirectFetch, `fetch("/users");`)).toEqual([
    { file: "index.ts", line: 1, column: 1, message: "Use the API client instead of fetch()" },
  ]);
});
```

Plugins need the npm launcher (`npx slop-police` or `pnpm exec slop-police`), which runs them in Node while the linter serves ASTs and types. Published plugin packages must ship JavaScript, since Node doesn't run TypeScript from `node_modules`.

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

macOS or Linux on arm64 or x64 (on Windows, use WSL), and Node.js 22.18 or later. The linter bundles its own TypeScript 7 compiler, so your tsconfigs need to work with TypeScript 7.

## License

MIT
