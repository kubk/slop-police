interface I { foo(): void }
class A implements I { foo() {} bar() {} }
const o: I = { foo() {} };
const arr: A[] = [];
arr.forEach((x) => x.bar());
