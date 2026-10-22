function View({ store }: { store: { close(): void } }) { return null; }
class S1 { close() {} u1() {} }
class S2 { close() {} u2() {} }
class S3 { close() {} u3() {} }
class S4 { close() {} u4() {} }
class S5 { close() {} u5() {} }
class S6 { close() {} u6() {} }
class S7 { close() {} u7() {} r() { return <View store={this} />; } }
declare const c: boolean;
let s5: S5 | undefined;
async function main() {
  const a = <View store={(0, new S1())} />;
  const b = <View store={c ? new S2() : new S2()} />;
  const d = <View store={(new S3() as S3)} />;
  const e = <View store={await Promise.resolve(new S4())} />;
  const g = <View store={(s5 = new S5())} />;
  const h = <View store={new S6()!} />;
  new S7().r();
}
main();
