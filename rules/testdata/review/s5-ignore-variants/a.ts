// slop-police-ignore
type A1 = Y;
//slop-police-ignore no-identity-type-alias
type A2 = Y;
// slop-police-ignore　no-identity-type-alias
type A3 = Y;
// slop-police-ignore	no-identity-type-alias
type A4 = Y;
// slop-police-ignore no-identity-type-alias -- reason
type A5 = Y;
// slop-police-ignore other-rule,no-identity-type-alias
type A6 = Y;
/* slop-police-ignore */
type A7 = Y;
// slop-police-ignore

type A8 = Y;
const k = 1; // slop-police-ignore
type A9 = Y;
// slop-police-ignore​
type A10 = Y;
// slop-police-ignore﻿
type A11 = Y;
// slop-police-ignore--x
type A12 = Y;
// slop-police-ignore -- no-identity-type-alias
type A13 = Y;
// slop-police-ignore no-identity-type-alias--
type A14 = Y;
// slop-police-ignore
/* x */ type A15 = Y;
/* x */ // slop-police-ignore
type A16 = Y;
// slop-police-ignore
// other
type A17 = Y;
function f() {
  // slop-police-ignore
  return cn("x");
}
const o = {
  // slop-police-ignore
  a: cn("x"),
  b:
  // slop-police-ignore
  cn("y"),
};
// slop-police-ignore type A18 = Y;
// slop-police-ignore 
type A19 = Y;
