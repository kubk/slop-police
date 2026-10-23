// TypeScript's API declarations use Symbol.dispose.
/// <reference lib="esnext.disposable" />
import type { Checker, Program } from "./typescript/dist/api/sync/api.js";
import type { Node, SourceFile, SyntaxKind } from "./typescript/dist/ast/index.js";
import type { NodesByKind } from "./typescript/nodes-by-kind.js";

export * from "./typescript/dist/ast/index.js";
export {
  ElementFlags,
  isBigIntLiteralType,
  isBooleanLiteralType,
  isClassOrInterfaceType,
  isConditionalType,
  isErrorType,
  isIndexedAccessType,
  isIndexType,
  isIntersectionType,
  isIntrinsicType,
  isLiteralType,
  isNumberLiteralType,
  isObjectType,
  isStringLiteralType,
  isStringMappingType,
  isSubstitutionType,
  isTemplateLiteralType,
  isTupleType,
  isTypeParameter,
  isTypeReference,
  isUnionType,
  NodeBuilderFlags,
  ObjectFlags,
  SignatureFlags,
  SignatureKind,
  SymbolFlags,
  TypeFlags,
  TypePredicateKind,
} from "./typescript/dist/api/sync/api.js";
export type {
  Checker,
  ConditionalType,
  IndexedAccessType,
  IndexType,
  InterfaceType,
  IntersectionType,
  IntrinsicType,
  LiteralType,
  ObjectType,
  Program,
  Signature,
  StringLiteralType,
  Symbol,
  TupleType,
  Type,
  TypeParameter,
  TypeReference,
  UnionType,
} from "./typescript/dist/api/sync/api.js";

export type JsonValue = string | number | boolean | null | JsonValue[] | { [key: string]: JsonValue };

/** Option defaults. The config's values must have the same JSON types. */
export type RuleOptions = Record<string, JsonValue>;

export interface LintedFile {
  /** The path relative to slop-police.json, with forward slashes. */
  readonly path: string;
  /** The absolute path. */
  readonly fileName: string;
  /** Whether the file matches the config's testFiles. */
  readonly isTest: boolean;
  readonly sourceFile: SourceFile;
}

export interface RuleContext<Options extends RuleOptions = {}> {
  /** The rule's option defaults, merged with the options from the config. */
  readonly options: Options;
  /** The file being visited. Undefined in finish. */
  readonly file: LintedFile;
  /** The type checker of the file's project. Undefined in finish. */
  readonly checker: Checker;
  /** The program of the file's project. Undefined in finish. */
  readonly program: Program;
  /**
   * Reports a finding at the start of node, which can be in any linted file.
   * `// slop-police-ignore` comments apply as for built-in rules.
   */
  report(node: Node, message: string): void;
}

type NodeOfKind<Kind extends string> = Kind extends keyof NodesByKind ? NodesByKind[Kind] : Node;

/**
 * Handlers by SyntaxKind name, called for every node of that kind in every
 * linted file, plus finish, called once after the last file.
 */
export type Visitor<Options extends RuleOptions = {}> = {
  [Kind in keyof typeof SyntaxKind]?: (node: NodeOfKind<Kind>, ctx: RuleContext<Options>) => void;
} & {
  finish?: (ctx: RuleContext<Options>) => void;
};

interface RuleBase<Options extends RuleOptions> {
  /** The name to enable the rule with in slop-police.json and to list in ignore comments. */
  name: string;
  /** Option defaults, which also set the types of the options the config can pass. */
  options?: Options;
}

export type Rule<Options extends RuleOptions = {}> = RuleBase<Options> &
  (
    | { visit: Visitor<Options>; create?: never }
    | {
        /** Called once per run, for rules that keep state across files. Returns the visitor. */
        create(ctx: RuleContext<Options>): Visitor<Options>;
        visit?: never;
      }
  );

export declare function defineRule<Options extends RuleOptions = {}>(rule: Rule<Options>): Rule<Options>;
