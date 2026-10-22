export class A { foo() { return 1; } bar() { return 2; } }
const { foo: bar } = new A();
bar();
