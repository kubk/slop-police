import { f } from "./f";
function View({ store }: { store: { close(): void } }) { return null; }
interface Store { (): number }
class Store {
  close() {}
  unused() {}
}
declare function isStore(x: unknown): x is Store;
if (isStore(f)) { const v = <View store={f} />; }
