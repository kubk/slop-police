export class Store { a() {} b() {} c() {} d() {} unused() {} }
type P1 = Pick<Store, "a" | ("b")>;
type P2 = Pick<typeof s, "c">;
declare const s: Store;
type S2 = Store;
type P3 = Pick<S2, `d`>;
namespace N { export type Pick<A, B> = A; }
type P4 = N.Pick<Store, "unused">;
