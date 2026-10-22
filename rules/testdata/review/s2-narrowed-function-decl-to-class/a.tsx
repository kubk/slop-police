function View({ store }: { store: { close(): void } }) { return null; }
interface Store { (): number }
class Store {
  close() {}
  unused() {}
}
declare function isStore(x: unknown): x is Store;
function g() { return 1; }
if (isStore(g)) { const v = <View store={g} />; }
