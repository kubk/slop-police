import { Store, cfg } from "../a/store";
function View({ store }: { store: { close(): void } }) { return null; }
export const v = <View store={new Store()} />;
new Store().viaB();
cfg.x;
