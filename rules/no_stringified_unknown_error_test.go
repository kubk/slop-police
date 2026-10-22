package rules_test

import (
	"slices"
	"testing"
)

var stringifiedErrorRules = obj{"rules": obj{"no-stringified-unknown-error": "error"}}

func TestStringifiedUnknownError(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{"fixture.ts": `
declare function run(): void;
declare function setError(message: string): void;

export function attempt() {
  try {
    run();
  } catch (error) {
    return { success: false, error: error instanceof Error ? error.message : String(error) };
  }
}

export function onFailure(err: unknown, e: unknown, cause: unknown) {
  setError(err instanceof Error ? err.message : String(err));
  const message = e instanceof Error ? String(e) : e.message;
  return (cause as Error) instanceof Error ? (cause!.message as string) : String(cause satisfies unknown);
}

export function describe(failure: unknown) {
  return failure instanceof DOMException || failure instanceof TypeError ? failure.message : String(failure);
}
`}, stringifiedErrorRules)
	want := []finding{
		{9, "Caught value error is flattened into a string"},
		{14, "Caught value err is flattened into a string"},
		{15, "Caught value e is flattened into a string"},
		{16, "Caught value cause is flattened into a string"},
		{20, "Caught value failure is flattened into a string"},
	}
	if got := lineMessages(results); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// Reported at the conditional.
	if got := locatedFindings(results[:1]); got[0].Column != 37 {
		t.Errorf("got %v, want column 37", got)
	}
}

func TestStringifiedUnknownErrorIgnored(t *testing.T) {
	t.Parallel()
	results := lintFindings(t, map[string]string{"fixture.ts": `
declare function run(): void;
declare const logger: { error(fields: object): void };

export function rethrow() {
  try {
    run();
  } catch (error) {
    throw error;
  }
}

export function keepStructure() {
  try {
    run();
    return { success: true };
  } catch (error) {
    logger.error({ error });
    return { success: false, error };
  }
}

export function otherShapes(failure: unknown, error: unknown, other: unknown) {
  // Plain instanceof checks only count for the usual error variable names.
  const a = failure instanceof Error ? failure.message : String(failure);
  const b = error instanceof TypeError ? error.message : String(error);
  const c = error instanceof Error ? error.message : JSON.stringify(error);
  const d = error instanceof Error ? error.stack : String(error);
  const e = error instanceof Error ? other.message : String(error);
  // slop-police-ignore no-stringified-unknown-error -- shown to the user as is
  const f = error instanceof Error ? error.message : String(error);
  return [a, b, c, d, e, f];
}
`}, stringifiedErrorRules)
	if len(results) != 0 {
		t.Errorf("got %v, want no findings", lineMessages(results))
	}
}
