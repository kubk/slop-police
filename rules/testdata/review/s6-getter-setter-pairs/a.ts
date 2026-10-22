export class Store {
  #v = 1;
  get v() { return this.#v; }
  set v(x) { this.#v = x; }
  get w() { return this.#v; }
  set w(x) {}
}
const s = new Store();
s.v = 2;
console.log(s.w);
