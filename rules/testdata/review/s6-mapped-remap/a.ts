class A { foo() {} bar() {} }
type G = { [K in keyof A as `get${Capitalize<K>}`]: A[K] };
declare const g: G;
g.getFoo();
type F = { [K in keyof A as K extends "bar" ? K : never]: A[K] };
declare const f: F;
f.bar();
