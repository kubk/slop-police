package rules_test

import (
	"slices"
	"testing"
)

var promiseFallbackRules = obj{"rules": obj{"no-promise-default-fallback": "error"}}

func TestPromiseDefaultFallback(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{"fixture.ts": `
declare function fetchConfig(): Promise<{ debug: boolean }>;
declare function report(error: unknown): void;
declare const logger: { error(...args: unknown[]): void };
declare const pending: Promise<string> | undefined;
declare const untyped: any;
interface Task<T> extends PromiseLike<T> {
  catch(handler: (error: unknown) => unknown): Task<T>;
}
declare const task: Task<number>;

export const a = fetchConfig().catch(() => null);
export const b = fetchConfig().catch(() => {});
export const c = fetchConfig().catch((error) => {
  console.error("config load failed", error);
  logger.error(error);
  return false;
});
export const d = fetchConfig().catch(function () {
  return;
});
export const e = fetchConfig().catch(() => (0 as const));
export const f = fetchConfig().catch(() => "");
export const g = fetchConfig().catch(() => ({}));
export const h = fetchConfig().catch(() => []);
export const i = pending?.catch(() => undefined);
export const j = untyped.catch(() => null);
export const k = task.catch(() => null);
`}, promiseFallbackRules)
	want := []finding{
		{12, "Promise rejection is replaced with null"},
		{13, "Promise rejection is swallowed by an empty catch handler"},
		{14, "Promise rejection is logged and replaced with false"},
		{19, "Promise rejection is replaced with undefined"},
		{22, "Promise rejection is replaced with 0"},
		{23, `Promise rejection is replaced with ""`},
		{24, "Promise rejection is replaced with {}"},
		{25, "Promise rejection is replaced with []"},
		{26, "Promise rejection is replaced with undefined"},
		{27, "Promise rejection is replaced with null"},
		{28, "Promise rejection is replaced with null"},
	}
	if got := lineMessages(results); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// Reported at the handler, like slop-scan.
	if got := locatedFindings(results[:1]); got[0].Column != 38 {
		t.Errorf("got %v, want column 38", got)
	}
}

func TestPromiseDefaultFallbackIgnored(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{"fixture.ts": `
declare function fetchConfig(): Promise<{ debug: boolean }>;
declare function report(error: unknown): void;
declare function handleError(error: unknown): null;

export const rethrown = fetchConfig().catch((error) => {
  throw error;
});
export const result = fetchConfig().catch(() => ({ ok: false, reason: "missing" }));
export const loggedOnly = fetchConfig().catch((error) => {
  console.error(error);
});
export const reported = fetchConfig().catch((error) => {
  report(error);
  return null;
});
export const twoReturns = fetchConfig().catch((error) => {
  if (error) return null;
  return null;
});
export const named = fetchConfig().catch(handleError);

// Not a promise: a query builder's own catch method.
class Query {
  catch(handler: () => unknown) {
    handler();
    return this;
  }
}
export const query = new Query().catch(() => null);

// slop-police-ignore no-promise-default-fallback -- the config is optional
export const optional = fetchConfig().catch(() => null);
`}, promiseFallbackRules)
	if len(results) != 0 {
		t.Errorf("got %v, want no findings", lineMessages(results))
	}
}
