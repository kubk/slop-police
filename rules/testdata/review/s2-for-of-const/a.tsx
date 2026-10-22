function View({ store }: { store: { close(): void } }) { return null; }
class Store {
  close() {}
  unused() {}
}
const stores = [new Store()];
for (const store of stores) { const v = <View store={store} />; }
