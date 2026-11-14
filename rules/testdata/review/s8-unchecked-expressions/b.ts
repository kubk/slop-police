import { Box, type Chain, Point } from "./a";

// The calls return an instance, though not every signature of the function
// says so.
function identity<T>(value: T): T { return value; }
function orPoint<T>(value: T | Point): T | Point { return value; }
function find<T>(value: T): T | null { return value; }
function box<T>(value: T): Box<T> { return new Box(value); }
function pick(kind: "number"): number;
function pick(kind: "point"): Point;
function pick(kind: string): number | Point { return kind === "point" ? Point.create() : 1; }

function takeTypeParameter(shape: { viaTypeParameter(): number }) { return shape; }
function takeReducedUnion(shape: { viaReducedUnion(): number }) { return shape; }
function takeOverload(shape: { viaOverload(): number }) { return shape; }
function takeThisType(shape: { viaThisType(): number }) { return shape; }
function takeStatic(shape: { viaStatic(): number }) { return shape; }
function takeNonNull(shape: { viaNonNull(): number }) { return shape; }
function takeGenericClass(shape: { viaGenericClass(): number }) { return shape; }
takeTypeParameter(identity(Point.create()));
takeReducedUnion(orPoint(Point.create()));
takeOverload(pick("point"));
takeThisType(Point.create().self());
takeStatic(Point.create());
takeNonNull(find(Point.create())!);
takeGenericClass(box(1));
function takeUnwrap(shape: { viaUnwrap(): number }) { return shape; }
function takeInterfaceThis(shape: { viaInterfaceThis(): number }) { return shape; }
function takeAwait(shape: { viaAwait(): number }) { return shape; }
function chained<C extends Chain>(link: C) { return link.chain(); }
takeUnwrap(new Box(Point.create()).unwrap());
takeInterfaceThis(chained(Point.create()));
Point.create().passSelf((shape) => shape);
Point.create().spread((shape) => shape);
export async function later() { takeAwait(await Promise.resolve(Point.create())); }

// The operators give one of their operands.
function maybe(): Point | undefined { return Math.random() > 0.5 ? Point.create() : undefined; }
function takeOr(shape: { viaOr(): number }) { return shape; }
function takeNullish(shape: { viaNullish(): number }) { return shape; }
function takeAnd(shape: { viaAnd(): number }) { return shape; }
function takeAssignment(shape: { viaAssignment(): number }) { return shape; }
function takeComma(shape: { viaComma(): number }) { return shape; }
function takeNestedTernary(shape: { viaNestedTernary(): number }) { return shape; }
let last: Point | undefined;
takeOr(maybe() || Point.create());
takeNullish((maybe() ?? Point.create()));
takeAnd(identity({}) && Point.create());
takeAssignment((last = Point.create()));
takeComma((maybe(), Point.create()));
takeNestedTernary(last ? identity(last) : (maybe() ?? Point.create()));
