function View({ store }: { store: { close(): void } }) { return null; }
interface Store { (): number }
class Store {
  close() {}
  unused() {}
}
declare function isStore(x: unknown): x is Store;
const f = () => 1;
if (isStore(f)) { const v = <View store={f} />; }
