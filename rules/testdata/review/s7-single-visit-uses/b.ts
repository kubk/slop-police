import { Point, Other } from "./a";

new Other().used();
const { viaBinding } = new Point(1);
const partial: Partial<Point> = { viaLiteral() { return 0; } };

function make(): Point { return new Point(1); }
function takeNew(shape: { viaNew(): number }) { return shape; }
function takeCall(shape: { viaCall(): number }) { return shape; }
function takeTernary(shape: { viaTernary(): number }) { return shape; }
takeNew(new Point(1));
takeCall(make());
takeTernary(Math.random() > 0.5 ? make() : new Point(2));

// The literals are not instances, but their elements are checked.
function takeArray(shapes: { viaArrayElement(): number }[]) { return shapes; }
function takeObject(shape: { point: { viaProperty(): number } }) { return shape; }
takeArray([make()]);
takeObject({ point: make() });
export const uses = [viaBinding, partial, typeof make()];
