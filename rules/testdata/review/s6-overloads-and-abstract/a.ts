export abstract class A {
  abstract x(): void;
  y(): void;
  y(a?: number) {}
  static z() {}
  static {}
  [Symbol.iterator]() { return [][Symbol.iterator](); }
  ["computed"]() {}
  "quoted"() {}
  123() {}
  #priv() {}
  async *gen() {}
  declare d: number;
}
