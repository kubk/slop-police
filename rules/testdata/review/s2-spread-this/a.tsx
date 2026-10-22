function View({ store }: { store: { close(): void } }) { return null; }
class Store {
  close() {}
  unused() {}
  render() { return <View store={{ ...this }} />; }
}
new Store().render();
