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

1. **Memory.** The peaks here are the peak memory footprint from `/usr/bin/time -l` on a 16 GB Mac, medians of three runs. Max RSS undercounts there because macOS compresses memory: tldraw shows 4.5 GB of RSS for a 7 GB footprint. Peaks repeat within about 7%. Times don't: the same binary took 3.5 s and 7.0 s on vinext in two sessions, so compare times only within one table.
   - **Cause:** every configured tsconfig is its own program with its own checker, each one checks the shared workspace packages again, and with 0.3.15 every checker was held for the whole run: 6–13 GB on the big monorepos. Measured on tldraw: about 3 GB is live at the end, about 1.1 GB of it ASTs and module resolution and about 1.9 GB checker state. The checker count barely matters: tldraw has 53 projects and 59 checkers, and changing `FilesPerChecker` changed nothing.
   - **0.3.16 released the checkers after every pass, and it didn't work.** `ForEachFile` and `AnyReference` took the projects in batches of about `Workers` checkers and released a batch's checkers when it was done, so every pass built every checker again. It had only been measured on a synthetic monorepo with `GOGC=100`. On the real ones, 0.3.15 → 0.3.16:

     | Repository      | Peak, GB    | Time, s     |
     | --------------- | ----------- | ----------- |
     | video-shotcraft | 0.55 → 0.56 | 0.34 → 0.35 |
     | vite            | 0.62 → 0.72 | 0.64 → 0.75 |
     | blume           | 1.69 → 1.71 | 1.33 → 1.63 |
     | cf              | 1.74 → 1.77 | 3.31 → 2.71 |
     | vinext          | 3.13 → 3.35 | 3.51 → 4.44 |
     | hyperframes     | 3.93 → 3.70 | 3.97 → 4.24 |
     | workers-sdk     | 6.40 → 8.21 | 9.08 → 13.3 |
     | tldraw          | 7.01 → 5.72 | 7.22 → 9.69 |
     | cloudflare-os   | 6.98 → 5.80 | 23.5 → 23.6 |
     | rakazo          | 8.36 → 8.47 | 13.2 → 17.4 |
     | openbot         | 11.8 → 12.4 | 19.7 → 50.8 |
     | qwen-code       | 13.0 → 13.0 | 31.3 → 28.8 |

     Under 1.5 GB of heap the GC was off (`cmd/slop-police/tune.go`), so a released checker was never collected and the one built in its place came on top. Small repositories got bigger and slower: memo-card (5 tsconfigs, 698 files) went from 1.03 GB and 0.81 s to 1.26 GB and 1.02 s, and to 1.40 GB with 4 cores, where its projects no longer fit one batch. Over 1.5 GB the GC ran with GOGC=200, which lets the heap triple before it collects, so less live data rarely meant a lower peak.

   - **What lowers the peak** is releasing checkers together with a GC that collects sooner. Peaks in GB with the checkers kept for the run or released between batches, by the GOGC used once the heap is over 1.5 GB:

     | Repository    | Kept, 200 (0.3.15) | Kept, 100 | Kept, 50 | Released, 200 | Released, 100 | Released, 50 |
     | ------------- | ------------------ | --------- | -------- | ------------- | ------------- | ------------ |
     | vinext        | 3.13               | 2.96      | 2.72     | 3.23          | 3.12          | 2.74         |
     | hyperframes   | 3.93               | 3.70      | 3.14     | 3.65          | 4.08          | 3.21         |
     | workers-sdk   | 6.40               | 5.64      | 5.09     | 8.31          | 5.68          | 4.72         |
     | tldraw        | 7.01               | 5.91      | 5.09     | 5.87          | 4.28          | 3.37         |
     | cloudflare-os | 6.98               | 7.33      | 6.72     | 5.81          | 6.21          | 4.83         |
     | rakazo        | 8.36               | 7.00      | 6.66     | 8.33          | 7.16          | 5.84         |
     | openbot       | 11.8               | 9.63      | 9.67     | 12.7          | 8.25          | 6.51         |
     | qwen-code     | 13.0               | 12.6      | 11.8     | 13.1          | 11.4          | 8.42         |

     Released with GOGC=50 took about as long as released with GOGC=200. Released costs time against kept: every pass builds the checkers again, about 40% on tldraw and workers-sdk and more than twice on openbot, whose 16 tsconfigs overlap on the same sources. `GOMEMLIMIT` is no substitute: `GOMEMLIMIT=6GiB` doubled openbot's time on 0.3.15 without lowering its peak, because the live data is over the limit.

   - **0.3.17 has a memory budget instead.** `tuneRuntime` in `cmd/slop-police/tune.go` sets `lint.MemoryBudget` to `SLOP_POLICE_MEMORY_GB`, or to a quarter of the machine's memory (of the container's limit when that is less). While the heap is under the budget the run spends memory to be fast: the GC is off, and the projects keep their checkers between passes (`forEachBatch` in `lint/context.go`). Once it is over, the GC runs with GOGC=50, and a batch releases the checkers of the projects outside it before it takes its own, while the heap is over the budget. The budget is where saving starts, not a cap: the big repositories peak over it. Peak in GB and time in seconds:

     | Repository      | 0.3.15       | Budget 4 GB (the default on 16 GB) | Budget 1.5 GB |
     | --------------- | ------------ | ---------------------------------- | ------------- |
     | video-shotcraft | 0.55, 0.33 s | 0.55, 0.35 s                       | 0.55, 0.33 s  |
     | vite            | 0.62, 0.65 s | 0.62, 0.66 s                       | 0.62, 0.66 s  |
     | blume           | 1.69, 1.38 s | 1.69, 1.13 s                       | 1.71, 1.35 s  |
     | cf              | 1.74, 3.38 s | 2.10, 3.76 s                       | 1.73, 2.76 s  |
     | vinext          | 2.94, 6.97 s | 4.13, 5.22 s                       | 2.75, 7.27 s  |
     | hyperframes     | 3.96, 5.19 s | 4.36, 4.36 s                       | 3.10, 5.49 s  |
     | workers-sdk     | 6.22, 8.03 s | 6.18, 10.5 s                       | 4.67, 13.6 s  |
     | tldraw          | 6.93, 6.84 s | 5.33, 8.06 s                       | 3.39, 11.1 s  |
     | cloudflare-os   | 6.99, 19.0 s | 6.54, 24.1 s                       | 4.84, 23.7 s  |
     | rakazo          | 8.43, 9.33 s | 5.75, 13.1 s                       | 5.12, 13.9 s  |
     | openbot         | 12.3, 20.9 s | 7.13, 43.0 s                       | 6.50, 45.0 s  |
     | qwen-code       | 12.1, 29.6 s | 8.54, 34.4 s                       | 8.70, 35.2 s  |

     Small repositories are back where 0.3.15 had them: memo-card takes 1.04 GB and 0.88 s, and 0.81 GB and 0.95 s with `SLOP_POLICE_MEMORY_GB=0.5`. Repositories between 1.5 GB and the budget take more memory than on 0.3.15, since the GC never starts (vinext 2.94 → 4.13 GB). blume, vinext and hyperframes were 16–25% faster for it in this session, but cf's times differ by more than that between identical runs. An 8 GB budget on the 16 GB machine lost to 0.3.15 in both memory and time on most of the big repositories (workers-sdk: 9.0 GB and 18.9 s), probably because the machine starts to compress and swap.

   - **0.3.18 turns `unused-methods` off by default.** The rule resolves every reference to every class member, in four passes over the projects, and that was most of a run's time on the big repositories and 0.7–2.8 GB of their peak. It is an optional rule now (`Optional` in `lint.Rule`): it runs only when the config lists it, as in `"rules": { "unused-methods": "error" }`. 0.3.17 with every rule against the 0.3.18 default, with the default budget of 4 GB:

     | Repository      | Peak, GB    | Time, s     | Findings    |
     | --------------- | ----------- | ----------- | ----------- |
     | video-shotcraft | 0.55 → 0.55 | 0.32 → 0.31 | 181 → 181   |
     | vite            | 0.62 → 0.47 | 0.60 → 0.36 | 99 → 92     |
     | blume           | 1.70 → 1.40 | 1.27 → 0.79 | 223 → 223   |
     | cf              | 2.10 → 2.07 | 2.30 → 2.19 | 101 → 101   |
     | vinext          | 4.11 → 3.10 | 3.16 → 2.39 | 407 → 399   |
     | hyperframes     | 4.37 → 3.33 | 4.01 → 2.65 | 1024 → 1020 |
     | workers-sdk     | 6.22 → 4.77 | 11.4 → 7.00 | 1171 → 1111 |
     | tldraw          | 5.27 → 3.33 | 7.68 → 2.88 | 1035 → 992  |
     | cloudflare-os   | 6.58 → 3.74 | 24.5 → 8.92 | 1162 → 1148 |
     | rakazo          | 5.79 → 5.14 | 14.8 → 5.90 | 1116 → 1107 |
     | openbot         | 7.32 → 5.80 | 57.1 → 15.8 | 3106 → 3064 |
     | qwen-code       | 8.53 → 7.17 | 35.9 → 11.3 | 4993 → 4929 |

     The findings that went away are the rule's own.

   - **0.3.19 stopped checking types that the rules don't need.** `unused-exports`, `unused-object-keys` and `unused-enum-members` ask the checker for the symbol of every identifier named like one of their candidates. For a plain name the checker answers from scopes and imports. For a member of a value, as in `user.name`, it has to work out the type of `user`, and with thousands of export names in a repository that type-checked a large part of it: on openbot, 4% of the names that `unused-exports` resolved took 98% of its time. Such a member is a top-level declaration only when the value is a module object, like the result of `import()` or a namespace that was passed on. So the rules now skip the members of values, except the names that a module object used as a value can have. `HasModuleObjectValue` in `lint/modules.go` finds those objects from the syntax, and `isValueMember` in `rules/astutil.go` is the check. Members of namespaces and enums (`ns.name`, `Enum.A`) are resolved as before. 0.3.18 against 0.3.19, with the same findings on every repository:

     | Repository      | Peak, GB    | Time, s     |
     | --------------- | ----------- | ----------- |
     | video-shotcraft | 0.55 → 0.54 | 0.31 → 0.29 |
     | vite            | 0.47 → 0.46 | 0.38 → 0.34 |
     | blume           | 1.40 → 0.94 | 0.79 → 0.66 |
     | cf              | 2.06 → 0.93 | 2.22 → 0.90 |
     | vinext          | 3.10 → 3.07 | 2.41 → 2.35 |
     | hyperframes     | 3.33 → 3.11 | 2.49 → 2.33 |
     | workers-sdk     | 4.76 → 4.62 | 6.37 → 5.56 |
     | tldraw          | 3.34 → 3.15 | 2.85 → 2.61 |
     | cloudflare-os   | 3.74 → 2.45 | 7.94 → 4.53 |
     | rakazo          | 5.11 → 4.70 | 5.34 → 4.49 |
     | openbot         | 5.83 → 4.30 | 14.4 → 4.20 |
     | qwen-code       | 7.27 → 7.12 | 11.1 → 8.67 |

     memo-card went from 1.26 GB and 1.04 s on 0.3.16 to 0.81 GB and 0.59 s.

   - **0.3.26 makes `unused-class-members` resolve a name once per file.** The rule (the former `unused-methods`) sent one find-all-references request per candidate that the first pass left unused, and each request looked up every occurrence of the name in every file of every program with the candidate. These numbers are from a Linux sandbox with 2 CPUs and 4 GB (a budget of about 1 GB), with `unused-class-members` on, as max RSS, so they don't compare with the tables above. Medians of interleaved runs, with the same findings at every step (tldraw 719, vite 421):

     | Step                                                 | tldraw, s | tldraw peak, GB | vite, s | vite peak, GB |
     | ---------------------------------------------------- | --------- | --------------- | ------- | ------------- |
     | 0.3.25                                               | 132       | 2.04            | 1.3–1.7 | 0.70          |
     | One search per file for all names                    | 21.0      | 1.63            | 1.4–1.6 | 0.38          |
     | A shared file is searched in one program             | 11.5      | 1.51            | 1.24    | 0.34          |
     | Literals and primitive operators aren't type-checked | 8.9       | 1.37            |         |               |
     | One queue of checkers across projects                | 7.7–8.2   | 1.27–1.30       | 0.99    | 0.33          |
     | Structural uses in the visit for referenced members  | 7.1–7.5   | 1.28–1.34       | 0.92    | 0.34          |
     | Calls that return no instance aren't type-checked    | 6.8–7.0   | 1.27–1.30       | 0.92    | 0.34          |

     The table is 0.3.26: 0.3.27 has the structural uses in a pass of their own again (see below), which is the row before it plus the last one's gain. With `SLOP_POLICE_MEMORY_GB=8` the last row takes 4.9–5.0 s and 1.8 GB on tldraw. Of the 132 s, the find-all-references requests took 125 s. The run's CPU profile has 255 s: 141 s in the GC and 100 s in the search itself.
     - `collectRelatedReferences` in `rules/unused_class_members.go` is the search now. It visits each file once, resolves the identifiers named like a remaining candidate, and relates them the way find-all-references does (`memberRelations`). `ForEachProgramFile` in `lint/context.go` gives it the files of a program, which include the ones outside the analyzed files.
     - Programs that share a candidate's file share most of its other files too. A file is searched for a group of candidates in one of the group's programs, the one whose project analyzes it when there is one (`memberSearchGroup`).
     - `forEachChecker` in `lint/context.go` hands the checkers of all projects to the workers from one queue. With batches of projects, the workers waited for the slowest checker of each batch.
     - 0.3.26 ran the structural uses in the first visit of each file, where they had been a pass of their own, so that a run over its budget built the checkers one time less. 0.3.27 took that back, because it slowed down the runs that stay under their budget. In the first visit the rule doesn't know yet which classes will have unused members left, so it checks the expressions of every file that names a class with candidates. As a pass of its own it visits only the projects with such a class, and checks only the files that name one. On memocard-private (691 files, 5 tsconfigs, through the launcher with its plugin), where the first visit leaves almost nothing unused, medians of five interleaved runs:

       | Budget                             | 0.3.25          | 0.3.26          | 0.3.27          |
       | ---------------------------------- | --------------- | --------------- | --------------- |
       | 4 GB (the default on 16 GB)        | 2.42 s, 1.00 GB | 2.54 s, 1.05 GB | 2.34 s, 1.00 GB |
       | 1 GB (the default on this sandbox) | 2.75 s, 1.00 GB | 3.65 s, 1.12 GB | 2.62 s, 1.00 GB |

       With the 1 GB budget, 0.3.26's extra memory took the run over the budget, and the rules after it built their checkers again. tldraw pays for the pass with its 1 GB budget, 8.2 s instead of 7.2 s, and nothing with one of 8 GB. Measure a change to the rule on a repository like memocard too, where the rule finds nothing: tldraw and vite alone didn't show this.

     - A structural use needs the type of the expression, and checking a call for it infers type arguments and checks the arguments. `mayBeInstance` and `returnsNoInstance` skip the expressions that can't be an instance from their syntax or from the signatures of the called function: 21,300 checked calls became 2,315 on tldraw.
     - What is left on tldraw is about 6 s of the rule in an 8.2 s run: about 4 s for the first visit and the structural uses, and about 2 s for the related references. Most of it is the checker resolving calls. The run does four GC cycles of 0.7–0.9 s once it is over the budget.
     - Tried and not kept:
       - Searching an analyzed file only in the program of its project: 6% faster, but wrong when another program resolves the file's imports to other files (fixture `s4-paths-differ`).
       - One visit of each file for all rules: the time became bimodal (6.1–7.7 s against 6.5–7.5 s) and the peak rose by 60 MB.
       - A relations cache per checker: 2.6 GB.
       - A full check of each file before the queries (`GetDiagnostics`): slower, and 0.6 GB more.
       - One checker per project with the largest first: a higher peak for a gain within noise.
       - A GC goal that follows the budget instead of GOGC=50: no gain.
       - Searching for all candidate names in the first visit, to drop the second: 3.9 s instead of 1.9 s for the search.
       - Skipping names whose candidates are all used: wrong for aliases such as `export import bar = A.foo`.
     - `SLOP_POLICE_PROFILE=<dir>` writes `cpu.pprof` and `heap.pprof`. The samples have a `rule` label, since the stacks are cut off below the rule's functions: `go tool pprof -tagfocus=rule=unused-class-members`.

   - **Still to look at:**
     - With `unused-class-members` on, a run over its budget still builds the checkers three times, once for each of the rule's visits. tldraw takes 8.2 s with a 1 GB budget and about 5 s with one of 8 GB, where the checkers are kept and the GC stays off. The structural uses can't join the first visit as they are (see above): they would need a cheap way to tell which files to check before the first visit is over. openbot, where releasing checkers doubled the time, wasn't measured again.
     - Runs in parallel each spend a quarter of the machine before they start saving. Give them a `SLOP_POLICE_MEMORY_GB`.
     - Reading the memory on Linux (`/proc/meminfo` and the cgroup limits) has a test for the parsing, but wasn't run on Linux.
     - The ASTs and module resolution of all programs stay live for the whole run, since later passes and plugins read them.
2. **Speed on big repos:** with every rule, 24–57 s for openbot, cloudflare-os and qwen-code on 0.3.17. Most of it was `unused-methods` (find-references and structural uses), which is off by default since 0.3.18, and then `unused-exports` checking the types of values, which 0.3.19 stopped: see the tables above. What is left of the 4–9 s:
   - Loading the programs: 2–6 s on the big repositories, and nearly all of workers-sdk's 5.6 s.
   - `no-duplicate-code`: 0.8 s on openbot, 1.3–2.1 s on qwen-code.
   - Members of values that still need types. A file that a module object used as a value re-exports with `export *` has its export names checked everywhere. rakazo's `packages/core/src/index.ts` is loaded with `import()` in tests and has `export * from "./display-name.js"`, which exports `displayName`, so every `user.displayName` is type-checked (1 s of CPU). That is how a use like `(await import("@rakazo/core")).displayName` is told from the others. The names of a module whose namespace is used as a value are checked too, although the module ends up used whole (rakazo's `rpc.ts`, through a `typeof` of its namespace in a test, 0.6 s of CPU). Resolving the namespace imports before the rest would settle that.
3. **`no-pass-through-getter`** reports getters that keep a mutable `#field` read-only from outside (qwen 64, openbot 34, cloudflare-os ~14), getters with a setter, and `override` getters. Replacing these with a field would make it writable, so maintainers won't accept them. Idea: skip a getter when the class assigns the field outside the constructor, or has a setter of the same name.
4. **`no-pass-through-wrapper`** still reports these, by design or by gap:
   - wrappers over module-private state, like `const byId = new Map(); export function get(id) { return byId.get(id) }`. That's inconsistent with the `this.items.get()` exemption.
   - delegation to `this.#fn(x)` or deeper paths like `this.#options.store.list()` (`isPrivateMemberDelegation` handles only `this.#x.m()`)
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
