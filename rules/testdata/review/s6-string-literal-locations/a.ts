export class Store { a() {} b() {} c() {} d() {} e() {} unused() {} }
declare const s: Store;
s["a"]();
s[`b`]();
type K = Store["c"];
const { "d": dd } = s;
function g<T extends keyof Store>(k: T) {}
g("e");
