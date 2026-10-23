import { isDeepStrictEqual } from "node:util";
import { SyntaxKind } from "./typescript/dist/ast/index.js";

// Marks rules made by defineRule, so plugins can export helpers next to them.
// Symbol.for, so that rules from another copy of slop-police count too.
const ruleBrand = Symbol.for("slop-police.rule");

export function defineRule(rule) {
  const name = rule?.name;
  if (typeof name !== "string" || !/^[^\s,]+$/.test(name)) {
    throw new TypeError(`Rule name must be a non-empty string without spaces or commas, got ${JSON.stringify(name)}`);
  }
  if ((rule.visit === undefined) === (rule.create === undefined)) {
    throw new TypeError(`Rule ${name} must have either visit or create`);
  }
  if (rule.create !== undefined && typeof rule.create !== "function") {
    throw new TypeError(`Rule ${name}: create must be a function`);
  }
  // The defaults go through the binary, which merges the config's options
  // into them, as JSON.
  const { options } = rule;
  if (
    options !== undefined &&
    (typeof options !== "object" || options === null || Array.isArray(options) || !isDeepStrictEqual(JSON.parse(JSON.stringify(options)), options))
  ) {
    throw new TypeError(`Rule ${name}: options must be an object of JSON default values`);
  }
  if (rule.visit !== undefined) {
    visitorHandlers(name, rule.visit);
  }
  return Object.freeze({ ...rule, [ruleBrand]: true });
}

export function isRule(value) {
  return value?.[ruleBrand] === true;
}

// visitorHandlers checks a visitor and returns its handlers as [kind, handler]
// pairs, where kind is a SyntaxKind or "finish".
export function visitorHandlers(ruleName, visitor) {
  if (typeof visitor !== "object" || visitor === null) {
    throw new TypeError(`Rule ${ruleName}: the visitor must be an object`);
  }
  return Object.entries(visitor).map(([key, handler]) => {
    const kind = key === "finish" ? key : SyntaxKind[key];
    if (kind !== "finish" && typeof kind !== "number") {
      throw new TypeError(`Rule ${ruleName}: unknown node kind ${key} in the visitor`);
    }
    if (typeof handler !== "function") {
      throw new TypeError(`Rule ${ruleName}: visitor ${key} must be a function`);
    }
    return [kind, handler];
  });
}
