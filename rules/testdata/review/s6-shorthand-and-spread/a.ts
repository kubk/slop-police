class A { foo() {} bar() {} baz() {} }
const a = new A();
const { foo } = a;
const o = { foo, ...a };
const { bar: renamed } = a;
renamed();
const baz = 1;
const p = { baz };
