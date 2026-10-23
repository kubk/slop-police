import { createRequire } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { isRule, visitorHandlers } from "./rule.js";
import { API } from "./typescript/dist/api/sync/api.js";

// LintError is a failed run, with the exit code to report it with: 2 for
// config errors, 1 for others.
class LintError extends Error {
  constructor(message, exitCode) {
    super(message);
    this.exitCode = exitCode;
  }
}

// run lints root with its plugins, prints the report and returns the exit
// code, like the binary does without plugins.
export async function run(binary, root, plugins) {
  try {
    const result = lint(binary, root, await loadPlugins(root, plugins));
    process.stdout.write(`${result.output}\n`);
    return result.exitCode;
  } catch (error) {
    if (error instanceof LintError) {
      console.error(error.message);
      return error.exitCode;
    }
    throw error;
  }
}

// loadPlugins imports the plugins (paths relative to root, or package names)
// and returns the rules they export, as a rule or an array of rules.
async function loadPlugins(root, plugins) {
  const rules = new Map();
  for (const plugin of plugins) {
    let exports;
    try {
      exports = await import(resolvePlugin(root, plugin));
    } catch (error) {
      throw new LintError(`Unable to load plugin ${plugin}: ${error.message}`, 2);
    }
    const found = Object.values(exports).flat().filter(isRule);
    if (found.length === 0) {
      throw new LintError(`Plugin ${plugin} exports no rules (create them with defineRule)`, 2);
    }
    for (const rule of found) {
      const existing = rules.get(rule.name);
      if (existing && existing.rule !== rule) {
        throw new LintError(`Rule ${rule.name} is defined in both ${existing.plugin} and ${plugin}`, 2);
      }
      rules.set(rule.name, { rule, plugin });
    }
  }
  return [...rules.values()].map(({ rule }) => rule);
}

function resolvePlugin(root, plugin) {
  if (plugin.startsWith(".") || path.isAbsolute(plugin)) {
    return pathToFileURL(path.resolve(root, plugin)).href;
  }
  return pathToFileURL(createRequire(path.join(root, "package.json")).resolve(plugin)).href;
}

// lint runs the built-in rules enabled in root's config in the binary and the
// enabled ones among rules here, and returns the binary's report: {results,
// output, exitCode}. It throws a LintError when the run fails.
export function lint(binary, root, rules) {
  const api = new API({ tsserverPath: binary, cwd: root });
  try {
    // The API has no public way to send requests of its own.
    const { client } = api;
    const start = client.apiRequest("slopPolice/start", {
      rules: rules.map((rule) => ({ name: rule.name, options: rule.options ?? {} })),
    });
    if (start.error) {
      throw new LintError(start.error, start.exitCode);
    }
    const findings = start.rules.length > 0 ? runRules(api, start, rules) : [];
    return client.apiRequest("slopPolice/finish", { findings });
  } finally {
    api.close();
  }
}

// runRules visits every analyzed file once, calling the enabled rules'
// handlers for each node, and returns their findings.
function runRules(api, start, rules) {
  const snapshot = api.updateSnapshot({ openProjects: [...new Set(start.files.map((file) => file.project))] });
  const byName = new Map(rules.map((rule) => [rule.name, rule]));
  const findings = [];
  const handlers = []; // [rule name, ctx, handler] by SyntaxKind
  const finishers = [];
  let current; // the file being visited, and its project

  for (const { name, options } of start.rules) {
    const rule = byName.get(name);
    const ctx = {
      options,
      get file() {
        return current?.file;
      },
      get checker() {
        return current?.project.checker;
      },
      get program() {
        return current?.project.program;
      },
      report(node, message) {
        if (typeof node?.id !== "string") {
          throw new TypeError("report() takes a node of a linted file");
        }
        findings.push({ rule: name, node: node.id, message: String(message) });
      },
    };
    let visitor = rule.visit;
    if (rule.create) {
      try {
        visitor = rule.create(ctx);
      } catch (error) {
        throw ruleError(name, "in create", error);
      }
    }
    for (const [kind, handler] of visitorHandlers(name, visitor)) {
      if (kind === "finish") {
        finishers.push([name, ctx, handler]);
      } else {
        (handlers[kind] ??= []).push([name, ctx, handler]);
      }
    }
  }

  const visit = (node) => {
    const list = handlers[node.kind];
    if (list) {
      for (const [name, ctx, handler] of list) {
        try {
          handler(node, ctx);
        } catch (error) {
          throw ruleError(name, `on ${location(current.file, node)}`, error);
        }
      }
    }
    node.forEachChild(visit);
  };
  for (const file of start.files) {
    const project = snapshot.getProject(file.project);
    const sourceFile = project.program.getSourceFile(file.fileName);
    current = { file: { path: file.path, fileName: file.fileName, isTest: file.isTest, sourceFile }, project };
    visit(sourceFile);
  }
  current = undefined;
  for (const [name, ctx, finish] of finishers) {
    try {
      finish(ctx);
    } catch (error) {
      throw ruleError(name, "in finish", error);
    }
  }
  return findings;
}

function ruleError(name, where, error) {
  return new LintError(`Rule ${name} failed ${where}: ${error?.stack ?? error}`, 1);
}

function location(file, node) {
  const { line, character } = file.sourceFile.getLineAndCharacterOfPosition(node.getStart(file.sourceFile));
  return `${file.path}:${line + 1}:${character + 1}`;
}
