function View({ store }: { store: { close(): void } }) { return null; }
class Store {
  close() {}
  unused() {}
}
function f<T extends Store>(t: T) { return <View store={{ ...t }} />; }
f(new Store());
