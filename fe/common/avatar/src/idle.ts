/* The "Idle animations" switch (Team settings › General): whether a live
   blob avatar with nothing to do plays its idle fidgets (blob/motion/
   fidget.ts). Page-wide and read every frame, so flipping it needs no
   re-render: the bundle that loads the user's settings calls
   setIdleAnimations, everyone else keeps the default (on). */

let on = true;

export const idleAnimationsOn = () => on;

export function setIdleAnimations(v: boolean) {
  on = v;
}
