function View({ store }: { store: { close(): void } }) { return null; }
class Store {
  close() {}
  unused() {}
}
const map: Record<string, Store> = {};
for (const key in map) { const v = <View store={map[key]} />; const w = <View store={key as any} />; }
