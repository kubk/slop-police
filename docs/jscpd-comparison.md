# no-duplicate-code vs jscpd

`no-duplicate-code` replaces the `lint:jscpd` scripts in the projects under `~/projects`. This file records how it compares with jscpd: what was measured, every difference with a concrete example, what jscpd does that we don't, and how to migrate each project. It is written for the next agent who changes the rule. Rerun the comparison (see [Reproducing](#reproducing)) after any change to tokenizing or matching.

Measured on 2026-10-25 with jscpd 5.3.2 (bd1.ai, task-cracker and the others) and 5.1.2 (memo-card). Repo states: memo-card `3b858f09`, bd1.ai `8d2155977`, expense-tracker `561eade`, egor-roxie `0d61931`, git-filter `b0de726`, mobx-log `2019d50`, task-cracker `20ba242`. "memo-before" is memo-card at `acb4d9d2^`, the parent of "Reduce duplicated code and add duplicate lint checks", the commit where the duplicates jscpd found were fixed. The numbers are for the rule as it is now, which compares types. The first version stripped them like `--cross-formats js-ts` and had an `isolate` option; see [Types](#7-types) and [What jscpd does that we don't](#what-jscpd-does-that-we-dont) for why both went.

## Summary

- On the same TS/TSX files, at the same thresholds, every real copy jscpd 5.3.2 reports without `--cross-formats` is also reported by us, except duplicated import blocks, which we skip on purpose.
- Every other clone that only jscpd reports is a jscpd bug: the reported copy ends one token past where the copies stop matching, which can push it over `--min-lines` (see [jscpd ends a copy one token late](#1-jscpd-ends-a-copy-one-token-late)). jscpd 5.1.2 has a second, worse bug that stretches copies across unrelated code.
- The projects ran jscpd with `--cross-formats js-ts`, which strips types. Against that, we miss copies that differ only in their types (1 on memo-before) and report copies that are short without their types or are all types (10 at 10 lines, such as the same `getStatusVariant` in two bd1.ai files).
- We find real copies that jscpd can't: identical JSX at a different indentation (bd1.ai and expense-tracker had them).
- jscpd's `--skip-isolated` changed nothing in the two projects that used it, so there's no equivalent. Nothing needs a per-rule folder ignore; see [Migration](#migration).

Clone pairs on the same files (ours `minLines` N = jscpd `--min-lines` N−1, both with 50 tokens):

| jscpd 5.3.2 | jscpd clones | same pair found by us | jscpd only | ours only |
|---|---|---|---|---|
| ours 10 / jscpd 9 | 65 | 58 | 7 (6 one token late, 1 import block) | 16 (6 JSX indentation, 10 `.ts` ↔ `.tsx`) |
| ours 6 / jscpd 5 | 107 | 96 | 11 (6 one token late, 3 import blocks, 2 paired differently) | 20 (8 JSX indentation, 11 `.ts` ↔ `.tsx`, 1 paired differently) |
| ours 10 / jscpd 9 `--cross-formats js-ts` | 66 | 58 | 8 (6 one token late, 1 differs only in types, 1 type test) | 16 (6 JSX indentation, 10 with types) |
| ours 6 / jscpd 5 `--cross-formats js-ts` | 103 | 93 | 10 (6 one token late, 2 import blocks, 1 differs only in types, 1 type test) | 23 (8 JSX indentation, 15 with types) |

"`.ts` ↔ `.tsx`": without `--cross-formats`, jscpd doesn't compare a `.ts` file with a `.tsx` one, such as memo-card's landing `proPlanSection.tsx` with the frontend's `translations/en.ts`. "Paired differently": the same block in 3+ files (memo-card's `review_interval` in `ar.ts`, `fa.ts`, `uk.ts`, `en.ts`) is reported for different pairs of them.

## How jscpd 5 works

jscpd 5 is a Rust rewrite (npm package `jscpd`, binary per platform). Its source, under `rust/crates/` in github.com/kucherenko/jscpd, read at tags `v5.1.2` and `v5.3.2`:

- `cpd-tokenizer/src/javascript.rs`: tokens are oxc parser tokens (comments and whitespace skipped). A token's hash is `xxh3(text) ^ kind`, so tokens compare by exact text: renaming anything breaks a copy, and literals count by value. These are the same tokens as the TypeScript scanner's, so token counts match ours. One exception: JSX text that is only whitespace is a token too, indentation included.
- `cpd-tokenizer/src/ts_strip.rs`: with `--cross-formats js-ts`, TypeScript files drop the tokens of erasable syntax, so code that differs only in types still matches. That covers type annotations with their `:`, type parameters and arguments with `<>`, interfaces, type aliases, `as T`, `satisfies T`, `!`, `<T>x`, optional `?`, definite `!`, `this` parameters, type-only imports and exports, overload signatures, abstract and `declare` members and declarations, and the modifiers `public`, `private`, `protected`, `readonly`, `abstract`, `override` and `declare`. Parameter properties keep their modifiers. One quirk: for `export interface X {}` only the interface's span is dropped, so a stray `export` token stays. Without `--cross-formats`, nothing is stripped, and `.ts` and `.tsx` are compared only within their own format.
- `cpd-core/src/detect.rs`: a polynomial rolling hash over windows of `min_tokens` tokens. Files are processed in path order. The store keeps the first occurrence of each window hash. A clone opens at a window found in the store and grows by one token for each next window that is also found. A secondary pass (at most 2 stored occurrences per hash, plus a line-coverage filter) adds pairs among 3+ copies.
- `--min-lines N` keeps a clone when `end.line - start.line >= N`, so N + 1 lines, and only the first fragment is checked. The console prints `end - start + 1` as "lines".

## How no-duplicate-code works

See `rules/no_duplicate_code.go`.

- **Tokens.** We walk the AST. A token node (identifier, literal, keyword, JSX text, template part, regex) is one token, and the scanner fills in the punctuation and keywords between child nodes. Types are tokens like the rest of the code, as in jscpd without `--cross-formats`, but `.ts` and `.tsx` files are compared with each other.
- **Skipped code.** Import, `import =` and export-list declarations produce no tokens. JSX text is whitespace-collapsed, and whitespace-only JSX text is dropped. Code under a `// slop-police-ignore no-duplicate-code` comment becomes a single zero-width token that matches nothing.
- **Matching.** Files are sorted by path. Every window hash links to the previous window with the same hash. For a window, we walk up to 16 earlier occurrences, extend each match token by token, and keep the longest (the earliest on ties). A copy is reported once, at the later copy, and the windows inside it aren't matched again.
- **Lines.** `minLines` counts every line a copy spans. Either copy may meet it, since the two can be formatted differently.

jscpd flags and their slop-police equivalents:

| jscpd | slop-police |
|---|---|
| `--min-lines 10` | `"minLines": 11` (the default) |
| `--min-tokens 50` | `"minTokens": 50` (default) |
| `--cross-formats js-ts` | none: types are compared, and `.ts` is always compared with `.tsx` |
| `--ignore "**/*.test.*"` | `testFiles` (tests are neither reported nor compared) |
| `--ignore "<glob>"` | top-level `ignore` |
| `--skip-isolated "a\|b"` | none (it changed nothing where it was used) |
| scanned paths (`packages/api/src …`) | the tsconfigs in `projects` (usually more files) |
| `jscpd:ignore-start/end` | `// slop-police-ignore no-duplicate-code` above a statement, member or property |
| `--threshold`, `--exit-code`, `--reporters` | none; any finding is an error or a warning |

## Differences, with examples

### 1. jscpd ends a copy one token late

In `detect.rs` (5.1.2 and 5.3.2), clones from the secondary pass set their end with `spans.get(token_a + min_tokens)`, one past the window's last token at `token_a + min_tokens - 1`. The primary path uses `match_len - 1` and is correct. A secondary-pass clone (code that exists 3+ times) therefore includes the first token that doesn't match. When that token is on a later line, the clone grows by 1–2 lines and can cross `--min-lines`.

Every one of these 9 jscpd-only clones (from the `--cross-formats js-ts` runs) was checked by taking jscpd's two fragments and comparing their tokens: each has `tokens + 1` tokens, and the last one differs.

| repo | jscpd fragment (first copy) | jscpd's last token, per copy | real copy |
|---|---|---|---|
| memo-card | `card-form-store.ts:328-337` ↔ `:369-378`, 86 tokens | `}` vs `options` | 328-336 ↔ 369-377 |
| memo-card | `create-mock-card-preview-form.ts:28-37` ↔ `deck-form-store.ts:142-151`, 56 tokens | `(` vs `getCardForm` | 28-36 ↔ 142-150 |
| memo-card | `api/src/db/schema.ts:567-572` ↔ `:629-634`, 59 tokens | `lastReviewDate` vs `reviewedAt` | 567-571 ↔ 629-633 |
| memo-card | `upsert-user-db.ts:13-19` ↔ `get-users-to-notify.ts:6-12`, 66 tokens | `export` vs `const` (the stray `export` of an erased `export type`) | 13-17 ↔ 6-10 |
| memo-card | `library-schemas.ts:76-81` ↔ `:102-107`, 57 tokens | `cards` vs `}` | 76-80 ↔ 102-106 |
| bd1.ai | `app/src/db/schema.ts:321-330` ↔ `:421-430`, 68 tokens | `type` vs `message` | 321-329 ↔ 421-429 |
| bd1.ai | `app/src/lib/admin-actions.ts:48-57` ↔ `:81-90`, 51 tokens | `if` vs `try` | 48-55 ↔ 81-88 |
| memo-before | `card-form-store.ts:319-328` ↔ `:360-369`, 86 tokens | `}` vs `options` | 319-327 ↔ 360-368 |
| memo-before | `deck-form-store.ts:412-422` ↔ `:441-450`, 54 tokens | `return` vs `}` | 412-420 ↔ 441-449 |

bd1.ai `packages/app/src/lib/admin-actions.ts`. jscpd's fragment text (from `--reporters json`) is everything below except the second copy's ending:

```ts
// 48-58
export const banUserAction = createServerFn({ method: "POST" })
  .validator(userIdSchema)
  .handler(async ({ data }) => {
    const admin = await getAdminSession();

    if (!admin) {
      return { error: true, message: "Unauthorized" };
    }
// ↑ the 51 tokens from `=` to here are the copy
    if (admin.session.user.id === data.userId) {   // jscpd's fragment ends after this `if`

// 81-91
export const unbanUserAction = createServerFn({ method: "POST" })
  ...same 7 lines...
    try {                                          // …and after this `try`
```

memo-card `packages/frontend/src/screens/deck-form/card-form/store/card-form-store.ts`. The copy ends at `})),`; the next tokens are `}` and `options`:

```ts
// 328-337                                        // 369-378
front: this.cardForm.front.value,                 front: this.cardForm.front.value,
back: this.cardForm.back.value,                   back: this.cardForm.back.value,
example: this.cardForm.example.value || null,     example: this.cardForm.example.value || null,
answerType: this.cardForm.answerType.value,       answerType: this.cardForm.answerType.value,
answers: this.cardForm.answers.value.map((a) => ({ answers: this.cardForm.answers.value.map((a) => ({
  id: a.id,                                         id: a.id,
  text: a.text.value,                               text: a.text.value,
  isCorrect: a.isCorrect.value,                     isCorrect: a.isCorrect.value,
})),                                              })),
},                                                options: this.cardForm.options.value,
```

What it means: with `--min-lines 9` jscpd flags these 9-line copies, while ours needs `minLines: 9`. Matching jscpd here isn't a goal; the copies are 9 lines long.

### 2. jscpd 5.1.2 stretches copies across unrelated code

In 5.1.2 a clone keeps growing while each next window exists anywhere in the store, without checking that it continues the same earlier copy. Fixed in 5.3.2. memo-card's `lint:jscpd` still pins `^5.1.2`.

Fixture (`a.ts`, `a2.ts`, `b.ts`):

```ts
// a.ts
export const banUser = createServerFn({ method: "POST" })
  .validator(userIdSchema)
  .handler(async ({ data }) => {
    const admin = await getAdminSession();

    if (!admin) {
      return { error: true, message: "Unauthorized" };
    }

    if (admin.id === data.userId) {
      return 1;
    }
  });
// b.ts: same first 8 lines, then
    try {
      await unban();
    } catch {}
  });
// a2.ts: `export const other = createServerFn({ method: "GET" })` + same 7 lines + the `try` block of b.ts
```

`jscpd . --min-lines 1 --min-tokens 20` gives the following.

5.1.2:
```
a.ts [1:56 - 8:6] (8 lines, 44 tokens)   ↔ a2.ts [1:53 - 8:6]
a.ts [1:1 - 11:16] (11 lines, 68 tokens) ↔ b.ts [1:1 - 13:6]     ← wrong: a.ts and b.ts differ after line 8
```

5.3.2:
```
a.ts [1:56 - 8:6]  ↔ a2.ts [1:53 - 8:6]
a.ts [1:1 - 8:6]   ↔ b.ts [1:1 - 8:6]
a2.ts [4:42 - 13:6] ↔ b.ts [4:42 - 13:6]
```

We match 5.3.2 here. On memo-before, the bug made jscpd 5.1.2 report `library-service.ts` 726↔782 as 11 lines (really 10: `, changes: … new Map(` in 726-735 and 782-791). The user fixed it in acb4d9d2 anyway; we report it at `minLines: 10`.

### 3. Only the first copy's lines count in jscpd

Because jscpd checks only `fragment_a`, a copy is missed when the first copy happens to be formatted on fewer lines. memo-card at `acb4d9d2`, `packages/frontend/src/screens/deck-form/deck-form/store/card-list-store.ts`: lines 235-244 (10 lines) and 258-268 (11 lines, the copy has a blank line inside) share `}); const { deck, cardsToReview } = result; runInAction(() => {…}); this.clearSelection(); } catch (e) { notifyError({ e, info:`. jscpd `--min-lines 10` misses it; we report it at `minLines: 11`. The duplicate is gone at memo-card HEAD.

### 4. JSX at a different indentation

jscpd hashes whitespace-only JSX text with its indentation, so the same markup nested one level deeper never matches. We collapse JSX whitespace.

Fixture (also `TestNoDuplicateCodeJsxIndentation`): a 13-line `<section>` inside `export const A = () => (…)`, and the same section wrapped in a `<div>` in `B`. jscpd 5.1.2 and 5.3.2 give `Found 0 clones`; with the same indentation in both files they report it (13 lines, 108 tokens). We report it in both cases.

Real copies jscpd 5.3.2 misses at `--min-lines 9`, all reported by us at `minLines: 10`:

- bd1.ai: `packages/app/src/components/admin/batch-messages-table.tsx:95-106` ↔ `ai-runs-table.tsx:92-103`, the same user filter:
  ```tsx
  <Select value={currentUserId || "all"} onValueChange={handleUserChange}>
    <SelectTrigger className="w-[250px]">   {/* w-[200px] in ai-runs-table: the copy starts after it */}
      <SelectValue placeholder="All users" />
    </SelectTrigger>
    <SelectContent>
      <SelectItem value="all">All users</SelectItem>
      {users.map((user) => (
        <SelectItem key={user.userId} value={user.userId}>
          {user.email || user.userId}
        </SelectItem>
      ))}
    </SelectContent>
  </Select>
  ```
- bd1.ai: `packages/app/src/components/batch-message-display.tsx:650-662` ↔ `582-594` (same file).
- bd1.ai: `packages/app/src/components/chat/generic-tool-view.tsx:50-60` ↔ `chat-client.tsx:404-414`.
- expense-tracker: `packages/frontend/src/pages/transactions/transaction-form-screen.tsx:238-251` ↔ `create-transaction-screen.tsx:193-206`, the expense/income `<Tabs>`:
  ```tsx
  >
    <TabsList className="grid w-full grid-cols-2">
      <TabsTrigger value="expense">
        {t("transactionTypeExpense")}
      </TabsTrigger>
      <TabsTrigger value="income">
        {t("transactionTypeIncome")}
      </TabsTrigger>
    </TabsList>
  </Tabs>
  ```
- expense-tracker: `transaction-form-screen.tsx:391-402` ↔ `packages/frontend/src/pages/accounts/account-form-screen.tsx:311-322`.
- memo-before: `packages/frontend/src/ui/cards-to-review.tsx:13-22` ↔ `screens/shared/deck-row-with-cards-to-review/deck-row-with-cards-to-review.tsx:29-38` (two `<CardsToReviewCount>`s).
- At `minLines: 6`, also bd1.ai `paid-plan-card.tsx:53-59` and `upgrade-modal.tsx:110-116` ↔ `free-plan-card.tsx:16-22` (the feature `<ul>`).

### 5. Imports

We skip import and export-list declarations. Files that use the same modules list them the same way, and that isn't copy-pasted logic. jscpd reports memo-card `packages/frontend/src/screens/deck-review/deck-preview.tsx:1-9` ↔ `folder-review/folder-preview.tsx:1-9` (9 identical import lines, 52 tokens) at `--min-lines 5`. Imports also widen jscpd's copies: at memo-before, `get-my-info-decks-with-cards-db.ts` ↔ `get-my-decks-with-cards-db.ts` is 3-25 for jscpd (imports included). For us it's 9-25: the `DeckSortFields` type and the duplicated `compareDeckSort`, which acb4d9d2 moved into `compare-deck-sort.ts`.

### 6. A third partial copy splits first-occurrence matching

Matching only the first occurrence of each window, as jscpd does, breaks when a third file holds part of the copy. At memo-before, `deck-form-store.ts:145-156` copies `card-form-store.ts:111-122`, the card-to-form mapping. `card-form/create-mock-card-preview-form.ts`, which sorts before both, repeats its last ~55 tokens (`null), answers: createAnswerListField(card.answers ? … : [],`). The first occurrence of those windows is in the mock file, so a first-occurrence matcher splits the copy into two pieces under 11 lines each. Our first implementation missed it this way. jscpd 5.1.2 found it by accident through bug 2. We now compare a window with up to 16 earlier occurrences and extend each one, so the copy is found in one piece.

### 7. Types

The projects ran jscpd with `--cross-formats js-ts`, which strips the erasable syntax listed under `ts_strip.rs`, and the first version of this rule did the same. It now compares types like any other code. On 2026-10-25 each feature was toggled off in a scratch build and run over the 7 projects at their pre-migration commits and over memo-before. This is what stripping types changed (lines from the 10-line runs):

- **A copy only stripping finds (1).** memo-before `api/src/db/user/get-user-by-id.ts:6-16` ↔ `get-user-detailed-by-id.ts:28-38`: the same query, with a different name and return type (`Promise<UserDbType | null>` vs `Promise<UserDetailedDbType>`). Comparing types splits it into pieces under the threshold.
- **Copies stripping hides.** Without their types, these are under 50 tokens or 10 lines:
  - bd1.ai `routes/admin-batch-message-detail-route.tsx:18-35` ↔ `components/admin/batch-messages-table.tsx:24-41`: the same `getStatusVariant(status: BatchMessageJobStatus)` switch, ending in `return status satisfies never;`, defined in both files.
  - expense-tracker `frontend/src/translations/plural.ts:1-13` ↔ `api/src/translations/plural.ts:1-13` (the `PluralForms` type and `createPlural`), and `translations.ts:5-22` ↔ `4-21` (the `Translation` mapped type), copied between the API and the frontend.
  - memo-card `frontend/src/lib/mobx-query-lite/make-query.ts:290-304` ↔ `make-infinite-query.ts:75-89`: the same constructor and `makeAutoObservable<this, "getConfig" | "options" | "currentQuery">(…)` call.
  - memo-card `frontend/src/translations/ptBr.ts:36-51` ↔ `es.ts:39-54`: `review_interval`, with an inline `{ value: number; unit: ReviewIntervalUnit }` parameter type that every locale repeated.
  - memo-before: `lib/platform/telegram/telegram-platform.ts:95-109` ↔ `browser/browser-platform.ts:74-88` (`getTheme(): PlatformTheme`) and `deck-review/store/review-store.ts:166-176` ↔ `148-157`.
  - Noise: 2 in bd1.ai's generated `route-tree.gen.ts`, which belongs in `ignore` anyway.
- **A false copy from stripping (1).** memo-card `frontend/src/translations/translation-tags.test-d.ts:49-58` ↔ `40-49`, a type test that the `testFiles` globs don't match. Stripped, every `expectTypeOf<MissingKeys<typeof ru>>().toEqualTypeOf<never>();` becomes `expectTypeOf().toEqualTypeOf();`, so two different tests look the same.

With stripping and `isolate` gone, the rule went from 384 to 226 lines.

### 8. Smaller differences

- **Merged clones (5.1.2 only, bug 2).** bd1.ai `chat-functions.ts` 413-422 ↔ 437-447 hides a separate copy, 440-447 ↔ 387-393. 5.3.2 and we report the two separately.
- **Copy end in JSX.** jscpd ends `folder-actions.tsx` ↔ `deck-actions.tsx` (memo-before) one line later, because the whitespace JSX text after the last `}` is a token for jscpd.
- **Scope.** jscpd scans the paths it's given. We check every non-test file of the tsconfigs, so memo-card also gets `packages/landing`, `packages/frontend/playground` and `packages/frontend/admin`.

## What jscpd does that we don't

Measured, with impact on these repos:

- **Test files.** We skip them; jscpd finds 40 clones among memo-card's tests and 4 in expense-tracker's (`--min-lines 9`). All the projects already passed `--ignore "**/*.test.*"`, so nothing is lost.
- **Other languages and files outside tsconfigs** (JS, CSS, markdown, YAML, JSON). jscpd over whole repos found no real source clones there: only CI YAML, drizzle `meta/*_snapshot.json`, code samples in `.agents/skills/*/SKILL.md`, and bd1.ai's vendored `telegram/` fork and built `public/telegram` bundles. This would matter for a repo with plain `.js` sources.
- **Ignoring a range.** `jscpd:ignore-start/end` covers any range; our comment covers one statement, member or property.
- **Matching code that differs only in types** (`--cross-formats js-ts`). See [Types](#7-types): it found 1 copy we don't, hid 7 real ones and made up 1.
- **Pair filters** (`--skip-isolated`, `--skip-local`), `--threshold`, `--max-lines`, reporters and baselines. memo-card and bd1.ai passed `--skip-isolated` for their api/frontend and app/telegram-api packages, and an `isolate` option that copied it changed nothing there: no copies cross those packages. Its only effect was hiding memo-card's landing ↔ frontend copies, which jscpd never saw because it didn't scan landing. bd1.ai's `--threshold 1.5` never mattered (0 clones).
- **Per-rule file ignores.** Not needed. The one folder jscpd ignored that we'd report in is egor-roxie's shadcn `src/components/ui/**` (1 finding, `dropdown-menu.tsx:144-162` ↔ `98-116`). Adding it to egor-roxie's top-level `ignore` changes nothing for the other rules: "No problems found" either way.

## Migration

Every project runs jscpd as `lint:jscpd` in `package.json`, and nowhere else (CI runs `pnpm lint`). `memocard-public` and `expense-tracker-public` are git-filter exports of memo-card and expense-tracker (`package.json` and `slop-police.json` are synced), so they follow the private repos and aren't edited.

Per project:

1. Bump `slop-police` to 0.3.4 or later. From 0.3.4 every built-in rule runs unless the config turns it off, and `no-duplicate-code` defaults to `minLines: 11`.
2. Delete the `lint:jscpd` script and the `jscpd` devDependency, then run `pnpm install`.
3. Drop `rules` entries that only repeat the defaults (`"error"` without options).
4. Run `pnpm lint` and fix or ignore what's listed.

The default `minLines: 11` is one above the `--min-lines 10` every project but egor-roxie used, so the gate is as strict as before. `minLines: 10` would add the findings in the last column.

| project | jscpd today | config change | findings to fix | extra at `minLines: 10` |
|---|---|---|---|---|
| memo-card | `packages/{api,frontend}/src`, 10/50, js-ts, `--skip-isolated "packages/api\|packages/frontend"` | add landing's `proPlanSection.tsx` to `ignore` (below) | 21: `frontend/src/screens/deck-form/card-form/card-type-modal.tsx:29-47` ↔ `frontend/playground/modal-stories.tsx:126-144`; `landing/src/components/landing-page/footer.tsx` 110-124, 126-141, 144-156 ↔ 92-106; `make-query.ts` and `ptBr.ts` from section 7; 15 in landing's `proPlanSection.tsx` (below) | none |
| bd1.ai | `packages/{app,telegram-api}/src`, 10/50, js-ts, `--skip-isolated "packages/app\|packages/telegram-api"`, `--threshold 1.5` | add `"**/route-tree.gen.ts"` to `ignore` | 4: the JSX copies from section 4 and `getStatusVariant` from section 7 | 3× the drizzle `createdAt`/`updatedAt` columns in `app/src/db/schema.ts` (285, 466, 497 ↔ 250-259) |
| expense-tracker | 4× `packages/*/src`, 10/50, js-ts | none | 4: the JSX copies from section 4, and `plural.ts` and `translations.ts` from section 7 | none |
| egor-roxie | `.`, 5/50, ignores `routeTree.gen.ts`, `src/components/ui/**` | `"no-duplicate-code": ["error", {"minLines": 6}]`, and add `"src/components/ui/**"` to `ignore` | none | none |
| git-filter | `src`, 10/50 | none | none | none |
| task-cracker | `src`, 10/50, ignores `components/ui/**` | none | none | `src/stores/workspace-store.ts:700-709` ↔ `609-618` |
| mobx-log | `src`, 10/50 (`node_modules` isn't installed locally) | new `slop-police.json`: `{"projects": ["tsconfig.json"], "testFiles": ["**/*.test.{ts,tsx}"]}`, plus the `slop-police` devDependency and a `lint:slop-police` script | 1 from the other rules, which it hadn't run before: `colors.green` in `src/browser-logger/default-logger.ts` is never read | none |

memo-card's top-level `ignore` also lists `packages/landing/src/components/landing-page/proPlanSection.tsx`. jscpd never scanned landing, and it has 15 findings: `landing/src/components/landing-page/proPlanSection.tsx` copies `frontend/src/screens/pro/pro-page.tsx` and the pro-plan strings of the 5 frontend translation files. Those are real copies, but landing is React with framer-motion and the app is Preact with motion, each with its own translations, so sharing them is a refactor of its own. Until then the file is ignored. An ignore comment on each of its ~11 copied statements would have kept the other rules on it.

## Reproducing

Never modify the repos; work in the scratchpad.

1. **Build.** `go build -o $S/slop-police ./cmd/slop-police`.
2. **Config.** Per repo, a scratch dir with a `slop-police.json` copied from the repo. Make `projects` absolute paths, and prefix `ignore` globs that don't start with `**` with the relative path back to the repo. Set `"rules": {"no-duplicate-code": ["error", {"minLines": N}]}`. Output paths come out prefixed with that relative path; strip it.
3. **memo-before.** `git -C ~/projects/memo-card archive acb4d9d2^ | tar -x -C $S/memo-before`, then symlink memo-card's root and `packages/*/node_modules` into it.
4. **Same files for jscpd.** List the files slop-police analyzes (a throwaway test in `rules/` that prints `f.Source.FileName()` for every non-test `ctx.Files` entry after `lint.NewContext`). Copy them into `$S/cmp/<repo>/` under their repo-relative paths.
5. **Run jscpd.** `jscpd $S/cmp/<repo> --min-lines $((N-1)) --min-tokens 50 --no-gitignore --reporters json -o <out>`. Run it once with `--cross-formats js-ts` and once without. The projects no longer install jscpd; the public mirrors still do: `~/projects/expense-tracker-public/node_modules/.bin/jscpd` is 5.3.2 and `~/projects/memocard-public/node_modules/.bin/jscpd` is 5.1.2.
6. **Match pairs.** jscpd's `duplicates[].firstFile/secondFile` hold `name` (relative to the scanned dir), `startLoc.line` and `endLoc.line`. Our lines parse with `(\S+):\d+:\d+\s+\S+\s+no-duplicate-code\s+Lines (\d+)-(\d+) duplicate (?:lines |(\S+):)(\d+)-(\d+)`. Two clones are the same pair when both fragments overlap in line range, in either order.
7. **Explain each jscpd-only pair.** Slice both fragments' text by `startLoc.position`..`endLoc.position`, tokenize, and compare. A mismatch at the last token is bug 1.
