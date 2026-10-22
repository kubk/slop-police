function View({ store }: { store: { close(): void } }) { return null; }
interface Store { (): number }
class Store {
  close() {}
  unused() {}
}
declare function assertStore(x: unknown): asserts x is Store;
const f = () => 1;
assertStore(f);
const v = <View store={f} />;
