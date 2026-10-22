import { Store } from "./store";
function View({ store }: { store: { close(): void } }) { return null; }
export const v = <View store={new Store()} />;
