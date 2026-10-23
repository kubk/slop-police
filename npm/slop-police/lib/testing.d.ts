import type { Rule, RuleOptions } from "./index.js";

export interface Finding {
  /** 1-based. */
  line: number;
  /** 1-based, in UTF-16 code units. */
  column: number;
  message: string;
}

/** Runs rule alone on source, as index.ts in a strict TypeScript project. */
export declare function lint(rule: Rule<any>, source: string, options?: RuleOptions): Finding[];
/**
 * Runs rule alone on files (paths to contents), in a strict TypeScript project
 * unless files has a tsconfig.json.
 */
export declare function lint(
  rule: Rule<any>,
  files: Record<string, string>,
  options?: RuleOptions,
): (Finding & { file: string })[];
