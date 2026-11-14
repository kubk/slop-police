export class Point {
  constructor(public x: number) {}
  viaBinding() { return 1; }
  viaLiteral() { return 2; }
  viaNew() { return 3; }
  viaCall() { return 4; }
  viaTernary() { return 5; }
  viaArrayElement() { return 6; }
  viaProperty() { return 7; }
  unused() { return 8; }
}
// Named here, but every candidate of it is referenced.
export class Other {
  used() { return 1; }
}
