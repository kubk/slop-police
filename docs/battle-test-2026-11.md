# Battle test on 12 real repositories (November 2026)

Notes for the next agent working on slop-police. In November 2026 we ran slop-police on 12 popular open-source TypeScript repositories to find where it breaks, which false positives it produces, and which findings are worth upstream PRs. This file has the results, what was fixed in 0.3.15, what's still open, and the PR candidates.

## How it was run

- One subagent per repository, each following [INSTALL.md](../INSTALL.md) literally (the install itself was part of the test). The agents cloned with `--depth 1`, installed dependencies, wrote `slop-police.json`, ran it, and classified every finding as a true positive, a false positive (a slop-police bug), library public API or a config gap.
- They ran against the published 0.3.14. Where it crashed, they built a patched copy of the source to get findings.
- Clones, configs, raw outputs, minimal repros and per-repo reports were in the session scratchpad (`/private/tmp/claude-501/.../scratchpad/{repos,work,reports}`). It's temporary, so don't count on it still existing. To re-run, clone the repo again and let an agent set it up from INSTALL.md.
- **Memory warning:** a single run on a big monorepo takes 7–8 GB. Running 12 agents in parallel on a 16 GB machine thrashed it. Run heavy jobs (slop-police, `tsc --build`, repo builds) one at a time, for example through a `lockf` wrapper that all agents share.

| Repository                     | Kind                                                                              | Files              | Notes                                                                       |
| ------------------------------ | --------------------------------------------------------------------------------- | ------------------ | --------------------------------------------------------------------------- |
| haydenbleasel/blume            | library (Astro docs framework) and 2 apps                                         | ~1100              | `.astro`/`.mdx` import TS files; needs `astro sync` first                   |
| QwenLM/qwen-code               | product (CLI, daemon, web shell, VS Code extension) that also publishes libraries | ~7700              | 48 tsconfigs, workspace packages resolve through `dist/`                    |
| nightly-labs/openbot           | product (Electron, Workers, Expo, Bun)                                            | ~6400              | 16 tsconfigs overlapping on `packages/*/src`; runs knip in CI               |
| tldraw/tldraw                  | library SDK and apps                                                              | ~2850              | 53 tsconfigs, project references, 219 examples loaded by README frontmatter |
| heygen-com/hyperframes         | library monorepo, CLI, studio app                                                 | ~4700              | runs knip and fallow; tests excluded from every tsconfig                    |
| Vincentwei1021/video-shotcraft | skill/template kit plus a Vite and Remotion app                                   | ~300               | most TS lives in no tsconfig; copy-paste kit by design                      |
| elie222/rakazo                 | product (api, worker, web, Electron, Expo, Astro)                                 | ~3800              | `export *` barrels in internal packages                                     |
| vitejs/vite                    | library                                                                           | small after config | `playground/` treated as test fixtures                                      |
| cloudflare/cloudflare-os       | product (~20 Workers, SPA, gadget apps)                                           | ~1800              | 65 tsconfigs, codegen needed first                                          |
| cloudflare/workers-sdk         | libraries and CLIs (wrangler, miniflare, …)                                       | large              | 360 tsconfigs, `dist`-typed workspace packages                              |
| cloudflare/cf                  | product (the `cf` CLI)                                                            | ~420 analyzed      | 7k generated files ignored                                                  |
| cloudflare/vinext              | framework (Next.js API on Vite)                                                   | ~1000              | `next/*` shims are public API                                               |

## Results with 0.3.14

**It crashed on 9 of 12 repositories:** cf, vite, tldraw, blume, openbot, rakazo, vinext, workers-sdk and qwen-code.

- **Where:** `panic: Unexpected nil in getImmediateAliasedSymbol` from `markChain` in `rules/unused_exports.go`.
- **Trigger:** reading `default` from a dynamically imported CommonJS (`export =`) or JSON module, as in `(await import("electron-updater")).default` or `const { default: m } = await import("cjs")`, while any analyzed file has an `export default`. With esModuleInterop, the checker makes up a `default` alias symbol with no declarations, and `GetImmediateAliasedSymbol` panics on it.
- **Workarounds:** none. Files in `ignore` are still read for uses.
- **Exit code:** the Go runtime exits a panic with 2, which INSTALL.md says means an invalid config. Agents went looking for config problems.
- **Location:** the panic didn't name a file, so one agent had to bisect.

The other false-positive patterns, by how many repos they hit, were:

1. **Structural typing in `unused-methods`** (openbot ~41, workers-sdk ~33, vinext 20, qwen 18, cloudflare-os 15, hyperframes). A class passed or returned where an interface is expected, without `implements`, got all its methods reported. Only JSX attributes were handled.
2. **Cloudflare Workers RPC** (workers-sdk ~79, cloudflare-os 33, vinext 21, tldraw 2). Public methods of `WorkerEntrypoint`/`DurableObject`/`RpcTarget` subclasses are called by other Workers.
3. **`entry` only affected `unused-exports`.** Library public API was still reported by `unused-methods` (tldraw ~85, vinext ~70, vite 43, hyperframes 34), getters, wrappers, identity aliases and enum members.
4. **Chained `export *` from an entry wasn't followed** (workers-sdk 120, qwen 433). `index → export * from mid → export * from leaf` reported leaf's exports as unused.
5. **Methods the platform calls:** custom element callbacks (blume, hyperframes), `toJSON`, iterator `return`/`throw`, `[Symbol.asyncDispose]`/`toString()` reported as wrappers.
6. **`const Layout = layout; <Layout />`** reported by `no-redundant-value-alias` (vinext 12). JSX needs the capital.
7. **Project references:** imports resolve to the referenced project's `.d.ts` output, and `program.GetSourceFile` returned nil for it. Whole private packages were "never imported" (tldraw).

## What 0.3.15 fixed

| Fix                                                                                                                                                                                                                                                                                                                                                                                                                                | Where                                                                                                                           |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| The crash: `markChain` stops at alias symbols without declarations                                                                                                                                                                                                                                                                                                                                                                 | `rules/unused_exports.go`                                                                                                       |
| Crashes are reported as `slop-police crashed in <rule> on <file>: …` with the stack trace, and exit with 3, also through the plugin launcher                                                                                                                                                                                                                                                                                       | `lint/crash.go`, `lint/context.go` (`Parallel`, `ForEachFile`), `lint/lint.go`, `lint/host.go`, `npm/slop-police/lib/runner.js` |
| Entry points expose what they re-export through chains of `export *`. A dynamic import of a barrel still uses only the barrel's own exports. Spreading it through the barrel's `export *` hid 458 real findings on rakazo, where tests `await import()` barrels in mocks.                                                                                                                                                          | `rules/unused_exports.go`                                                                                                       |
| Imports through project references use the referenced sources (`GetSourceFileForResolvedModule`)                                                                                                                                                                                                                                                                                                                                   | `rules/unused_exports.go`                                                                                                       |
| Public instance members of Workers RPC classes, custom element callbacks, `toJSON`/`toString`/`valueOf`/`then`, and `next`/`return`/`throw` of self-iterating classes count as used                                                                                                                                                                                                                                                | `rules/unused_methods.go` (`calledByRuntime`, `isProtocolMember`)                                                               |
| `[Symbol.*]()` and conversion methods aren't reported as wrappers                                                                                                                                                                                                                                                                                                                                                                  | `rules/no_pass_through_wrapper.go` (`isShapeRequired`)                                                                          |
| Structural uses: an instance passed as an argument or JSX attribute, returned, assigned, stored in a typed place or an array, or cast with `as`/`satisfies` marks the members the target type has. The target is skipped when it's the class or one of its base classes.                                                                                                                                                           | `rules/unused_methods.go` (`collectStructurallyUsedMembers`)                                                                    |
| `entry` is a top-level config key, not an `unused-exports` option. The declarations an entry exports (through re-exports, `export *` and namespace exports, plus the classes exported classes extend) are public. `unused-methods`, `no-pass-through-getter`, `no-pass-through-wrapper`, `no-identity-type-alias`, `unused-enum-members` and `unused-object-keys` skip public API. Redundant `super` overrides are still reported. | `lint/public.go`, `lint/config.go`, `rules/astutil.go` (`isPublicMember`)                                                       |
| Capitalized aliases of lowercase values used as JSX tags aren't reported                                                                                                                                                                                                                                                                                                                                                           | `rules/no_redundant_value_alias.go`                                                                                             |
| INSTALL.md: list `main`/`exports`/`bin` only for published packages, and add a tsconfig that includes tests when every tsconfig excludes them                                                                                                                                                                                                                                                                                      | `INSTALL.md`                                                                                                                    |

The structural pass is the expensive one. `GetTypeAtLocation` on every argument and initializer cost about 20 s on qwen-code. So `instanceClass` finds the class from symbols instead: `new X()`, `this`, or a variable's or property's declared type. A full type check happens only in files whose name table mentions a class that still has unused members (casts and type guards name the class). Overall time stayed about the same, because find-references gets fewer candidates.

### Verification

The fixed build was run on all 12 repos with the agents' final configs, after moving `entry` to the top level. It produced no crashes, and no finding that wasn't in the agents' earlier runs. The true positives the agents had verified by hand are all still reported, for example tldraw's `AnalyticsService.dispose` ×6, openbot's `BrowserRecorder.isRecording`, and cloudflare-os's 6 `GitHubApi` methods.

| Repository      | Findings before → after | Main drops                                                                      |
| --------------- | ----------------------- | ------------------------------------------------------------------------------- |
| workers-sdk     | 1443 → 1171             | unused-exports −120 (chained `export *`), unused-methods 168 → 60               |
| qwen-code       | 5691 → 4993             | unused-exports −433 (public `export *` chains of core), unused-methods 199 → 64 |
| vinext          | 534 → 407               | unused-methods 84 → 8, enum members 9 → 0                                       |
| tldraw          | 1174 → 1035             | unused-methods 142 → 43, wrappers 58 → 32                                       |
| cloudflare-os   | 1240 → 1162             | unused-methods 75 → 14, exactly the 14 the agent called real                    |
| hyperframes     | 1105 → 1024             | unused-methods 44 → 4                                                           |
| vite            | 146 → 99                | unused-methods 45 → 7                                                           |
| blume           | 240 → 223               | identity aliases 16 → 4, unused-methods 5 → 0                                   |
| openbot         | 3137 → 3106             | unused-methods 70 → 42                                                          |
| rakazo          | 1122 → 1116             |                                                                                 |
| cf              | 101 → 101               |                                                                                 |
| video-shotcraft | 181 → 181               |                                                                                 |

## Still open

1. **Memory.** Big monorepos peak at 7–7.7 GB RSS (tldraw, openbot, cloudflare-os, rakazo, workers-sdk, qwen-code), and up to 11.5 GB with the GC headroom. Measured on tldraw: about 3 GB is live at the end, about 1.1 GB of it ASTs and module resolution and about 1.9 GB checker state.
   - **Cause:** every configured tsconfig is its own program with its own checker, and each one checks the shared workspace packages again. The checker count barely matters: tldraw has 53 projects and 59 checkers, and changing `FilesPerChecker` changed nothing.
   - **GC tuning doesn't help:** GOGC=100 was unchanged, GOGC=50 saved 1.5 GB but was 27% slower, and `GOMEMLIMIT=6GiB` doubled openbot's time without lowering its peak, because the live data is over the limit.
   - **The real fix** is to run all rules project by project and release each project's checkers before the next, merging findings at the end. That's a redesign of `runRules` and of the rules that work across projects (find-references across `ProjectsContaining`, unused-exports uses).
2. **Speed on big repos:** 17–21 s for openbot, cloudflare-os and qwen-code. Most of it is `unused-methods`: find-references and structural uses.
3. **`no-pass-through-getter`** reports getters that keep a mutable `#field` read-only from outside (qwen 64, openbot 34, cloudflare-os ~14), getters with a setter, and `override` getters. Replacing these with a field would make it writable, so maintainers won't accept them. Idea: skip a getter when the class assigns the field outside the constructor, or has a setter of the same name.
4. **`no-pass-through-wrapper`** still reports these, by design or by gap:
   - wrappers over module-private state, like `const byId = new Map(); export function get(id) { return byId.get(id) }`. That's inconsistent with the `this.items.get()` exemption.
   - delegation to `this.#fn(x)` or deeper paths like `this.#options.store.list()` (`isPrivateMemberDelegation` handles only `this.#x.m()`)
   - wrappers that add a default parameter value (`f(x = "en") { return g(x) }`)
   - wrappers whose receiver is a parameter (`v.toLocaleString()`)
   - `accepts(id, id)`
   - callees that are a reassignable `let`
5. **Uses across workspace packages that resolve through `dist/*.d.ts` without project references** (workers-sdk, qwen-code) aren't counted. The consumer only sees the declaration file. Mapping it back would need declaration maps, or the repository needs `paths`/project references.
6. **The duplicate rules** can't be scoped. Templates and examples that are copied on purpose produce about 230 findings on tldraw, about 92 on video-shotcraft, and frozen protocol codecs on openbot. `no-duplicate-function` also groups functions that differ only in literals (seeded hash constants) or in which same-named local helpers they call. It hashes names, not symbols.
7. **Smaller false positives:**
   - Vite query imports (`./x.worker?worker`) get no file edge, so the worker looks never imported.
   - Static members read through `this.constructor as Ctor` in an analyzed base class: tldraw's 24 `static children()`. `constructorTypes` only searches classes outside the analyzed files.
   - Method names passed as strings typed `keyof this`.
   - `type Assert<A extends B, B> = A` is reported as an identity alias.

**What not to add to INSTALL.md:** the maintainer's rule is to keep it short and only cover what agents get wrong silently. Agents work out repo-specific setup themselves: building `dist` packages, codegen, `.astro`/`.vue`/`.mdx` imports, React Native platform files, knip configs as a source of entries. Never tell agents to override a user's policy, like pnpm's or bun's `minimumReleaseAge`, which installed 0.3.13 in 8 repos. That's the user's choice.

## Upstream PR candidates

Each was verified by grepping the whole repository, including md, json, yaml and html. None was built or tested after the removal. Before opening one, re-check against the current default branch, run the repository's typecheck and tests, and mention slop-police in one neutral line rather than as the headline. Products are better targets than libraries.

1. **cloudflare/workers-sdk** (best: the maintainers asked for it). Delete 8 re-export shims in `packages/wrangler` that nothing imports, about 100 lines. They were left behind when code moved to pages-functions and deploy-helpers, and three carry "TODO: remove these re-exports":
   - `src/pages/functions/{filepath-routing,identifiers,routes-consolidation,routes}.ts`
   - `src/deployment-bundle/{capnp,secrets-validation}.ts`
   - `src/environments/index.ts`
   - `src/utils/diff-json.ts`

   Follow-ups: `src/deployments.ts` (390 lines), `src/ai-search.ts` and `src/agent-memory/provisioning.ts`, which deploy-helpers has its own copies of. Also 9 small dead files: `d1/formatTimeAgo.ts`, `d1/options.ts`, `utils/mergeWithOverride.ts`, `NotImplementedError.ts`, miniflare's `browser-rendering/constants.ts`, and others.

2. **cloudflare/cloudflare-os.** Delete 14 dead workshop-frontend files, about 1,930 lines: `ProtectedRoute`, `VendorCard`, `RecentApps`, `TabButton`, six chat components, `data/{chat,sample}.ts` and others. Also remove 6 unused `GitHubApi` methods in `gatekeeper-github/src/github-api.ts`, about 80 lines.
3. **heygen-com/hyperframes.** Delete 5 dead files in `packages/producer/src/services/`: the `audioMixer`, `browserManager`, `chunkEncoder` and `parallelCoordinator` re-export shims, plus `compilationRunner.ts`, about 200 lines. The repo runs knip and fallow, which missed them. Also replace the two private copies of `readPositiveDimension` in `studio/src/components/editor/` with the exported one.
4. **QwenLM/qwen-code.**
   - Delete `packages/sdk-typescript/src/mcp/` (4 files, 496 lines). It's a stale copy of `src/daemon-mcp/`.
   - Delete 3 leftover files and their lines in `eslint.legacy-filenames.mjs`:
     - `cli/src/ui/hooks/useTomlMigration.ts` (empty; the repo's own `survey.md` flags it)
     - `core/src/memory/extractionPlanner.ts` (`export {}`)
     - `cli/src/ui/hooks/useRefreshMemoryCommand.ts`
   - Delete vscode-ide-companion's `webview/utils/diffUtils.ts`, `diffStats.ts` and `constants/loadingMessages.ts`, 355 lines.
5. **cloudflare/vinext.** `packages/vinext/src/index.ts:956-999` copies 4 tsconfig helpers from `config/tsconfig-paths.ts`, and the copies have drifted: only one wraps its result in `toSlash`. Reuse the exported ones, about −45 lines.
6. **nightly-labs/openbot.** Delete `packages/ui/src/error-message.ts` and `packages/ui/src/features/settings/AgentSelect.tsx` with their `exports` entries; knip misses them because it treats private packages' `exports` as entries. Also about 13 helpers with no reference anywhere (`truncateMiddle`, `isCustomProviderModelId`, `isUiBlockSpec`, `isPluginUrl`, …) and 12 unused methods (`BrowserRecorder.isRecording`, `MailboxStore.reactionFor`, `RemoteServerStore.isMuted`, `DrainScheduler.drainAgent`, …).
7. **Smaller ones:**
   - **tldraw:** dead internal helpers in `packages/tldraw`, about 100 lines, not in `api-report.api.md` (`TldrawUiButtonSpinner.tsx`, `clampToRange`, `getArrowLength`, `useFlatten`, `exportToString`, …). Also the overrides that only call `super`, `EmbedShapeUtil.getGeometry` and `PathBuilderGeometry2d.hitTestLineSegment`; the API report needs regenerating.
   - **blume:** 3 unused types in `packages/blume/src/reference/options.ts` (lines 68, 147, 184), and a shared `writeIfChanged` helper.
   - **cf:** 4 unused filter functions in `commands/hand-written.ts:380-405`.
   - **rakazo:** `apps/mobile/components/info-button.tsx`, `apps/mobile/lib/theme.ts`, and `packages/adapter-kit/src/registry.ts`, which only its own test uses.

   Skip vite (no dead code, only refactors) and video-shotcraft (a small template kit).

Opening a PR is outward-facing, so ask the maintainer before opening any of them.
