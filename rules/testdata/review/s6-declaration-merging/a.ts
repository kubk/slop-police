interface A { merged(): void }
class A { merged() {} own() {} }
declare const a: A;
a.merged();
namespace A { export const z = 1; }
