# Install slop-police

Instructions for a coding agent setting up [slop-police](https://github.com/kubk/slop-police) in the user's repository. slop-police is a type-aware linter for TypeScript that finds dead code, copy-pasted code and needless indirection. The [README](https://github.com/kubk/slop-police#readme) documents the config and every rule.

The goal is a config where every finding is real code to fix, not a gap in the config. Look at the repository before writing anything: the right `projects`, `ignore` and `entry` values come from its tsconfigs, package.json scripts and tool configs.

## 1. Check the requirements

slop-police runs on macOS and Linux (WSL on Windows) with Node.js 22.18 or later. It bundles its own TypeScript 7, so the repository doesn't need it installed, but its tsconfigs must work with it. If the repository already has a `slop-police.json`, keep the user's choices and only fill in what's missing.

## 2. Install

Add `slop-police` as a dev dependency with the repository's package manager, at the workspace root in a monorepo (`pnpm add -D -w`). Install all workspace dependencies too, so imports resolve.

## 3. Write `slop-police.json`

Put one config at the repository root, even in a monorepo: usages are counted across all listed projects, so code used only by another package still counts as used.

```json
{
  "projects": [
    "packages/api/tsconfig.json",
    "packages/web/tsconfig.app.json",
    "packages/web/tsconfig.node.json"
  ],
  "ignore": ["**/routeTree.gen.ts", "packages/web/src/components/ui/**"],
  "testFiles": ["**/*.test.{ts,tsx}", "**/*.spec.{ts,tsx}"],
  "rules": {
    "unused-exports": [
      "error",
      {
        "entry": [
          "packages/*/vite.config.ts",
          "packages/api/src/index.ts",
          "packages/api/scripts/*.ts",
          "packages/web/src/main.tsx",
          "packages/web/src/routes/**/*.tsx"
        ]
      }
    ]
  }
}
```

**`projects`**: every tsconfig that includes source files, tooling ones like `tsconfig.node.json` too, so config files get analyzed. A solution-style tsconfig (`"files": []` with `references`) includes no files and makes slop-police report nothing. List the tsconfigs it references instead.

**`testFiles`**: globs for the tests and test-only code the repository actually has, such as mocks, fixtures and `*.test-d.ts` files. Usages in tests count, but declarations in tests aren't reported as unused or duplicated.

**`ignore`**: only generated code (route trees, API clients) and vendored code the team copied in rather than wrote, such as shadcn components (see `components.json`). Files that `.gitignore` ignores are already skipped.

**`rules`**: every built-in rule is already on as an error. The usual entry is the `entry` option of `unused-exports`. With it, slop-police also reports files that nothing imports, and code used only by such files. That's where most dead code is found, so list every file that something outside the analyzed code loads:

- The source files behind `main`, `exports` and `bin` in package.json.
- Scripts run directly from package.json `scripts`, CI or docs.
- App and server entries: `<script src>` in `index.html`, a worker's `main` in its wrangler config, a server's start file.
- Files a framework loads by convention: file-based routes, Next.js `app/` and `pages/`, a router config.
- Config files and what they point to: `vite.config.ts`, `vitest.config.ts` and its `setupFiles`, `drizzle.config.ts`.
- Files imported from code outside the listed projects.

Tests are entry points already. An entry's own exports aren't checked, so don't list a file that something else already imports.

Keep the other rules at their defaults.

## 4. Run it and sort the findings

Run `npx slop-police` (or `pnpm exec slop-police`). Exit code 2 means an invalid config, explained on stderr. "No problems found" in a repository of any size usually means `projects` covers no files.

Go through the findings until only real code is left:

- **File is never imported from an entry point**: find out what loads the file. If something does (a script, a tool config, a framework convention), add it to `entry`. If nothing does, it's dead code.
- **Findings in generated or vendored code**: add those paths to `ignore`.
- **Everything else is a real finding.** Don't turn rules off, raise thresholds, widen `ignore` or add `// slop-police-ignore` comments to make the run pass. A `// slop-police-ignore <rule> -- <reason>` comment fits only code used in a way static analysis can't see, like a method called from the devtools console. Mention each one in your report.

## 5. Add it to the lint setup

Add a script that runs `slop-police`. If the repository has a `lint` script that runs several linters, add it there, for example as `lint:slop-police`.

## 6. Offer to replace knip and jscpd

If the repository uses them, tell the user that slop-police covers knip's unused exports, files and enum members, and jscpd's copy-paste detection. Offer to remove the dependencies, scripts, config files and CI steps. knip's checks for unused and unlisted dependencies have no equivalent in slop-police.

To keep a jscpd threshold, jscpd's `--min-lines N` is `no-duplicate-code`'s `minLines` N+1. The default is 11.

## 7. Report

Tell the user:

- The entries and ignores you chose, and why each one is there.
- The findings, counted by rule, with a few examples, and the files that look dead.

Ask before fixing findings. There can be many, and some code that looks dead may be work in progress.
