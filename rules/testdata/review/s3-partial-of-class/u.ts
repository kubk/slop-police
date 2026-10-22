import type { A } from "./a";
declare const x: Partial<A>;
x.foo?.();
declare const y: Omit<A, "zzz">;
y.foo();
