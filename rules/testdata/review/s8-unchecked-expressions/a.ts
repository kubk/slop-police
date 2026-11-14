export interface Chain {
  chain(): this;
}
export class Point implements Chain {
  chain(): this { return this; }
  constructor(public x: number) {}
  static create() { return new Point(0); }
  self(): this { return this; }
  viaTypeParameter() { return 1; }
  viaReducedUnion() { return 2; }
  viaOverload() { return 3; }
  viaThisType() { return 4; }
  viaStatic() { return 5; }
  viaUnwrap() { return 17; }
  viaThisCall() { return 18; }
  passSelf(take: (shape: { viaThisCall(): number }) => unknown) { return take(this.self()); }
  viaNonNull() { return 6; }
  viaOr() { return 7; }
  viaNullish() { return 8; }
  viaAnd() { return 9; }
  viaAssignment() { return 10; }
  viaComma() { return 11; }
  viaNestedTernary() { return 12; }
  viaInterfaceThis() { return 14; }
  viaSpread() { return 15; }
  viaAwait() { return 16; }
  spread(take: (shape: { viaSpread(): number }) => unknown) { return take({ ...this }); }
  unused() { return 13; }
}
export class Box<T> {
  constructor(public value: T) {}
  viaGenericClass() { return 1; }
  unwrap(): T { return this.value; }
  unused() { return 2; }
}
