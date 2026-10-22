function View({ store }: { store: { close(): void } }) { return null; }
function f() {}
namespace f { export function close() {} }
class K { close() {} unused() {} }
const x = <View store={f} />;
const y = <View store={new K()} />;
