import type { Rule, RuleOptions } from './index.js'

export interface Finding {
  /** The path relative to the project: index.ts for a source string. */
  file: string
  /** 1-based. */
  line: number
  /** 1-based, in UTF-16 code units. */
  column: number
  message: string
}

/**
 * Runs rule alone on source (as index.ts) or on files (paths to contents), in
 * a strict TypeScript project unless files has a tsconfig.json.
 */
export declare function lint(
  rule: Rule<any>,
  source: string | Record<string, string>,
  options?: RuleOptions,
): Finding[]
