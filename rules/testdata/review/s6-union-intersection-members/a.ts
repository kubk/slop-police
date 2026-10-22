class A { foo() {} only() {} }
class B { foo() {} }
declare const u: A | B;
declare const i: A & { x: 1 };
u.foo();
i.only();
