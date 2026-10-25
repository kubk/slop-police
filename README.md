<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.webp?v=3">
    <img src="assets/logo-light.webp?v=3" width="1000" alt="slop-police: Stop LLM crimes in your codebase">
  </picture>
</h1>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/readme-dark.webp?v=3">
    <img src="assets/readme-light.webp?v=3" width="1080" alt="slop-police flags an export only used in its own file, a function that just forwards, a variable that is just a rename, and a method that is never called">
  </picture>
</p>

## Installation

Paste this into your agent:

```text
Install and configure slop-police by following https://raw.githubusercontent.com/kubk/slop-police/main/INSTALL.md
```

Let your agent properly set it up so slop-police finds every real issue it can. For manual installation run `npm i -D slop-police`, add a [`slop-police.json`](#configuration) and run `npx slop-police`

## Comparison with other tools

|                                            | slop-police | knip | jscpd |
| ------------------------------------------ | :---------: | :--: | :---: |
| Unused exports and files                   |      ✓      |  ✓   |       |
| Unused enum members                        |      ✓      |  ✓   |       |
| Unused class methods                       |      ✓      |      |       |
| Unused object keys                         |      ✓      |      |       |
| Copy-pasted code                           |      ✓      |      |   ✓   |
| Helpers rewritten instead of reused        |      ✓      |      |       |
| Pass-through wrappers, getters and aliases |      ✓      |      |       |
| Flattened errors                           |      ✓      |      |       |

## Rules

### no-pass-through-wrapper

A function or method that only returns a call to another function.

```ts
function loadUser(id: string) {
  return api.loadUser(id);
}
```

### no-identity-type-alias

A type alias that only renames another type.

```ts
type User = { id: string };
type Member = User;
```

### no-duplicate-code

Copy-pasted blocks of code. Options: `minLines` (default `11`) and `minTokens` (default `50`).

```ts
// stripe-webhook.ts
const mailer = await Mailer.connect({
  host: "smtp.example.com",
  port: 465,
  secure: true,
  user: env.SMTP_USER,
  password: env.SMTP_PASSWORD,
});
await mailer.send({ to: customer.email, subject: "Your license" });

// reset-password.ts
const mailer = await Mailer.connect({
  host: "smtp.example.com",
  port: 465,
  secure: true,
  user: env.SMTP_USER,
  password: env.SMTP_PASSWORD,
});
await mailer.send({ to: user.email, subject: "Reset your password" });
```

### no-duplicate-function

The same function written again in several files, with different variable names. Option: `minFiles` (default `3`).

```ts
// users.ts
export async function loadUsers(id: string) {
  const response = await fetchJson("/users/" + id);
  if (!response.ok) throw new Error("failed to load users");
  return response.data;
}

// posts.ts
export async function loadPosts(postId: string) {
  const res = await fetchJson("/posts/" + postId);
  if (!res.ok) throw new Error("failed to load posts");
  return res.data;
}

// teams.ts
export async function loadTeams(teamId: string) {
  const result = await fetchJson("/teams/" + teamId);
  if (!result.ok) throw new Error("failed to load teams");
  return result.data;
}
```

### no-pass-through-getter

A getter that only returns another property.

```ts
class Store {
  private items: Item[] = [];
  get list() { return this.items; }
}
```

### no-redundant-value-alias

A variable that only renames another variable.

```ts
function render() {
  const user = getUser();
  const current = user;
  return current.name;
}
```

### no-single-string-cn

`cn()` with a single class string.

```tsx
<div className={cn("flex gap-2")} />
```

### no-stringified-unknown-error

Turning a caught error into a string with `error instanceof Error ? error.message : String(error)`.

```ts
try {
  await sendMessage(chatId, text);
} catch (error) {
  return { ok: false, error: error instanceof Error ? error.message : String(error) };
}
```

### unused-object-keys

Object keys that are never read.

```ts
export const links = {
  docs: "https://example.com/docs",
  legacy: "https://example.com/old", // links.legacy is never read
};
open(links.docs);
```

An index with a literal type reads only the keys in that type, so `labels[status]` with `status: "active" | "expired"` leaves a `trial` key unused. An index typed as `string` or `any`, or an object passed elsewhere, could read any key, so the object is skipped.

### unused-enum-members

Enum members that are never used.

```ts
enum Status {
  Active,
  Archived, // Enum member Status.Archived is never used
}
const isActive = (status: Status) => status === Status.Active;
```

### unused-exports

Exports that nothing imports, or that are only used in their own file.

```ts
// lib.ts
export type Options = { strict: boolean }; // Export Options is only used in its own file
export function parse(text: string, options?: Options) {}
export function format() {} // Export format is never used

// main.ts
import { parse } from "./lib";
```

If you want to be extra strict, list your entry points in the `entry` option. slop-police then also finds whole files that nothing imports from them or from tests. Code used only by such dead files counts as unused too.

```json
"unused-exports": ["error", { "entry": ["src/main.tsx", "src/routes/**/*.tsx", "vite.config.ts"] }]
```

### unused-methods

Class methods that are never called. Here `reset` is unused.

```ts
class Store {
  load() {}
  reset() {}
}
new Store().load();
```

## Configuration

Put a `slop-police.json` in the directory you run it from:

```json
{
  "projects": ["packages/api/tsconfig.json", "packages/frontend/tsconfig.app.json"],
  "testFiles": ["**/*.test.{ts,tsx}", "**/*.spec.{ts,tsx}"]
}
```

- `projects`: tsconfig files to analyze. Usages are counted across all of them, so a method used only by another package still counts as used.
- `ignore`: glob patterns (relative to the config) for files to skip entirely. Files that `.gitignore` ignores are skipped too.
- `testFiles`: glob patterns for tests. Usages inside tests count, but declarations inside tests are not reported.
- `rules`: you usually don't need it, since every built-in rule is already on as an error. It's for rule options, like the stricter `unused-exports` setup above, and for turning on plugin rules. A rule takes `"error"`, `"warn"`, `"off"` or `[severity, options]`.
- `plugins`: modules with [custom rules](#custom-rules), as paths relative to the config or package names.

## CI

Make CI fail when something slips through

```yaml
- run: npx slop-police
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

Plugins work only through the npm launcher (`npx slop-police`).

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

## Requirements

macOS or Linux on arm64 or x64 (on Windows, use WSL), and Node.js 22.18 or later. The linter bundles its own TypeScript 7 compiler, so your tsconfigs need to work with TypeScript 7.

## License

MIT
