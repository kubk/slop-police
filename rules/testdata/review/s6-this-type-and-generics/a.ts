export class Builder<T> {
  set(v: T): this { return this; }
  build() {}
  chain() { return this.set(null as any).build; }
  unused() {}
}
new Builder<number>().chain();
