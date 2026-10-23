// Tests for plugin rules. They need a slop-police binary in SLOP_POLICE_BINARY
// and the TypeScript API from scripts/build-typescript-api.mjs.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { defineRule, SyntaxKind } from "slop-police";
import { lint } from "slop-police/testing";
import noDirectFetch from "./fixtures/no-direct-fetch.ts";

assert.ok(process.env.SLOP_POLICE_BINARY, "SLOP_POLICE_BINARY must point to a slop-police binary");

const packageDir = path.resolve(import.meta.dirname, "..");
const fetchPlugin = fs.readFileSync(path.join(import.meta.dirname, "fixtures/no-direct-fetch.ts"), "utf8");

test("reports with lines and UTF-16 columns", () => {
  assert.deepEqual(lint(noDirectFetch, `const emoji = "🙂"; fetch("/a");\nexport const b = await fetch("/b");\n`), [
    { line: 1, column: 21, message: "Use the API client instead of fetch()" },
    { line: 2, column: 24, message: "Use the API client instead of fetch()" },
  ]);
});

test("passes options from the config, merged with the defaults", () => {
  const files = { "src/api/client.ts": `fetch("/a");`, "src/app.ts": `fetch("/b");` };
  assert.deepEqual(lint(noDirectFetch, files, { allowIn: "src/api/" }), [
    { file: "src/app.ts", line: 1, column: 1, message: "Use the API client instead of fetch()" },
  ]);
});

test("queries the checker of the file's project", () => {
  const rule = defineRule({
    name: "no-needless-nullish-fallback",
    visit: {
      BinaryExpression(node, ctx) {
        if (node.operatorToken.kind !== SyntaxKind.QuestionQuestionToken) return;
        const { checker } = ctx;
        const type = checker.getTypeAtLocation(node.left);
        const nullable = [checker.getUndefinedType(), checker.getNullType()].some((t) => checker.isTypeAssignableTo(t, type));
        if (!nullable) ctx.report(node.right, `${node.left.getText()} is ${checker.typeToString(type)}`);
      },
    },
  });
  const source = `
declare const count: number;
declare const name: string | undefined;
export const a = count ?? 0;
export const b = name ?? "";
`;
  assert.deepEqual(lint(rule, source), [{ line: 4, column: 27, message: "count is number" }]);
});

test("create keeps state across files and finish reports anywhere", () => {
  const rule = defineRule({
    name: "no-repeated-url",
    create(ctx) {
      const byUrl = new Map();
      return {
        StringLiteral(node) {
          if (node.text.startsWith("https://")) byUrl.set(node.text, [...(byUrl.get(node.text) ?? []), node]);
        },
        finish() {
          for (const [url, nodes] of byUrl) {
            if (nodes.length > 1) for (const node of nodes) ctx.report(node, `${url} is repeated`);
          }
        },
      };
    },
  });
  const files = {
    "a.ts": `export const a = "https://example.com"; export const b = "https://other.com";`,
    "b.ts": `export const c = "https://example.com";`,
  };
  assert.deepEqual(lint(rule, files), [
    { file: "a.ts", line: 1, column: 18, message: "https://example.com is repeated" },
    { file: "b.ts", line: 1, column: 18, message: "https://example.com is repeated" },
  ]);
});

test("defineRule checks the rule", () => {
  assert.throws(() => defineRule({ name: "a b", visit: {} }), /without spaces or commas/);
  assert.throws(() => defineRule({ name: "no-x" }), /must have either visit or create/);
  assert.throws(() => defineRule({ name: "no-x", visit: { CallExpresion() {} } }), /unknown node kind CallExpresion/);
  assert.throws(() => defineRule({ name: "no-x", options: [], visit: {} }), /options must be an object/);
  assert.throws(() => defineRule({ name: "no-x", options: { pattern: /x/ }, visit: {} }), /options must be an object of JSON default values/);
});

const tsconfig = JSON.stringify({
  compilerOptions: { strict: true, target: "ES2022", module: "ESNext", moduleResolution: "Bundler", noEmit: true },
  include: ["src/**/*.ts"],
});

// launch runs the npm launcher in a project made of files (with
// slop-police.json given as an object) and slop-police installed.
function launch(t, files) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "slop-police-plugins-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.mkdirSync(path.join(root, "node_modules"));
  fs.symlinkSync(packageDir, path.join(root, "node_modules/slop-police"), "dir");
  const all = { "tsconfig.json": tsconfig, ...files, "slop-police.json": JSON.stringify(files["slop-police.json"]) };
  for (const [name, content] of Object.entries(all)) {
    fs.mkdirSync(path.dirname(path.join(root, name)), { recursive: true });
    fs.writeFileSync(path.join(root, name), content);
  }
  const result = spawnSync(process.execPath, [path.join(packageDir, "bin/slop-police.js")], { cwd: root, encoding: "utf8" });
  return { code: result.status, stdout: result.stdout, stderr: result.stderr };
}

test("the launcher runs plugin rules with the built-in ones", (t) => {
  const result = launch(t, {
    "slop-police.json": {
      projects: ["tsconfig.json"],
      plugins: ["./rules/fetch.ts"],
      ignore: ["src/generated/**"],
      rules: { "no-identity-type-alias": "warn", "no-direct-fetch": ["error", { allowIn: "src/api/" }] },
    },
    "rules/fetch.ts": fetchPlugin,
    "src/app.ts": `
type User = { id: string };
export type Member = User;
export const a = fetch("/a");
// slop-police-ignore no-direct-fetch -- streams
export const b = fetch("/b");
`,
    "src/api/client.ts": `export const c = fetch("/c");`,
    "src/generated/client.ts": `export const d = fetch("/d");`,
  });
  assert.deepEqual(result, {
    code: 1,
    stdout: [
      "src/app.ts:3:13  warning  no-identity-type-alias  Type alias Member only renames User",
      "src/app.ts:4:18  error    no-direct-fetch         Use the API client instead of fetch()",
      "",
      "1 error, 1 warning",
      "",
    ].join("\n"),
    stderr: "",
  });
});

test("plugin rules that aren't enabled don't run", (t) => {
  const result = launch(t, {
    "slop-police.json": { projects: ["tsconfig.json"], plugins: ["./rules/throws.js"], rules: {} },
    "rules/throws.js": `
import { defineRule } from "slop-police";
export default defineRule({ name: "throws", visit: { SourceFile() { throw new Error("boom"); } } });
`,
    "src/app.ts": `export const a = 1;`,
  });
  assert.deepEqual(result, { code: 0, stdout: "No problems found\n", stderr: "" });
});

test("a rule that throws fails the run", (t) => {
  const result = launch(t, {
    "slop-police.json": { projects: ["tsconfig.json"], plugins: ["./rules/throws.js"], rules: { throws: "error" } },
    "rules/throws.js": `
import { defineRule } from "slop-police";
export default defineRule({ name: "throws", visit: { CallExpression() { throw new Error("boom"); } } });
`,
    "src/app.ts": `\nexport const a = String(1);`,
  });
  assert.equal(result.code, 1);
  assert.match(result.stderr, /^Rule throws failed on src\/app\.ts:2:18: Error: boom\n/);
});

test("config errors exit with 2", (t) => {
  const project = { projects: ["tsconfig.json"] };
  const cases = [
    [{ ...project, plugins: ["./missing.ts"] }, {}, /^Unable to load plugin \.\/missing\.ts: /],
    [{ ...project, plugins: ["./rules/none.js"] }, { "rules/none.js": "export const a = 1;" }, /^Plugin \.\/rules\/none\.js exports no rules \(create them with defineRule\)\n$/],
    [
      { ...project, plugins: ["./rules/a.ts", "./rules/b.ts"] },
      { "rules/a.ts": fetchPlugin, "rules/b.ts": fetchPlugin },
      /^Rule no-direct-fetch is defined in both \.\/rules\/a\.ts and \.\/rules\/b\.ts\n$/,
    ],
    [
      { ...project, plugins: ["./rules/fetch.ts"], rules: { "no-direct-fetch": ["error", { allowIn: 1 }] } },
      { "rules/fetch.ts": fetchPlugin },
      /^Rule no-direct-fetch option allowIn must be a string\n$/,
    ],
    [
      { ...project, plugins: ["./rules/alias.js"] },
      { "rules/alias.js": `import { defineRule } from "slop-police"; export default defineRule({ name: "unused-methods", visit: {} });` },
      /^Plugin rule unused-methods has the same name as a built-in rule\n$/,
    ],
  ];
  for (const [config, files, stderr] of cases) {
    const result = launch(t, { "slop-police.json": config, "src/app.ts": "export {};", ...files });
    assert.equal(result.code, 2, result.stderr);
    assert.equal(result.stdout, "");
    assert.match(result.stderr, stderr);
  }
});
