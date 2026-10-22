export class A {
  private p() {}
  protected q() {}
  private used() {}
  run() { this.used(); }
}
export class B extends A { r() { this.q(); } }
new A().run(); new B().r();
const Cx = class { private z() {} private zz() { this.z(); } };
