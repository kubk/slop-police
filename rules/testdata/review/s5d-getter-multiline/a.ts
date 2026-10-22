class A {
  w = 1;
  /** doc */
  get
  v() { return (this["w"]); }
  static get s() { return this.w; }
  get #p() { return this.w; }
  get q() { return this?.w; }
  get r() { return (((this)).w); }
}
