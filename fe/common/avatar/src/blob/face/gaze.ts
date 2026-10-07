export type Vec3 = { x: number; y: number; z: number };

export type EyePose = {
  x: number;
  y: number;
  ax: number;
  ay: number;
  dx: number;
  dy: number;
  depth: number;
};

export type Liveliness = {
  dYaw: number;
  dPitch: number;
  dRoll: number;
  driftX: number;
  driftY: number;
};

function spin(u: Vec3, v: Vec3, angle: number): [Vec3, Vec3] {
  const c = Math.cos(angle);
  const s = Math.sin(angle);
  return [
    { x: u.x * c + v.x * s, y: u.y * c + v.y * s, z: u.z * c + v.z * s },
    { x: v.x * c - u.x * s, y: v.y * c - u.y * s, z: v.z * c - u.z * s },
  ];
}

function rad(deg: number) {
  return (deg * Math.PI) / 180;
}

export function loopNoise(t: number, period: number, seed = 0) {
  const p = (t / period) * Math.PI * 2;
  return (
    0.55 * Math.sin(p + seed) +
    0.3 * Math.sin(2 * p + seed * 1.7 + 1.1) +
    0.15 * Math.sin(3 * p + seed * 2.3 + 2.4)
  );
}

export function getLiveliness(t: number, wander = 1): Liveliness {
  return {
    dYaw: (loopNoise(t, 11.3, 0.4) * 5.5 + loopNoise(t, 3.7, 2.1) * 1.6) * wander,
    dPitch: (loopNoise(t, 9.1, 1.3) * 4.2 + loopNoise(t, 4.3, 0.7) * 1.3) * wander,
    dRoll: loopNoise(t, 13.7, 3.2) * 2.2 * wander,
    driftX: loopNoise(t, 7.9, 1.9) * 0.006 * wander,
    driftY: loopNoise(t, 5.3, 0.3) * 0.007 * wander,
  };
}

export function eyePoses(
  yaw: number,
  pitch: number,
  roll: number,
  scale: number,
  split = 15.5,
): [EyePose, EyePose] {
  let forward: Vec3 = { x: 0, y: 0, z: 1 };
  let right: Vec3 = { x: 1, y: 0, z: 0 };
  let down: Vec3 = { x: 0, y: 1, z: 0 };

  let spun = spin(forward, right, rad(yaw));
  forward = spun[0];
  right = spun[1];

  spun = spin(down, forward, rad(pitch));
  down = spun[0];
  forward = spun[1];

  spun = spin(right, down, rad(roll));
  right = spun[0];
  down = spun[1];

  const build = (side: number): EyePose => {
    const [ef, er] = spin(forward, right, rad(split * side));
    return {
      x: ef.x * scale,
      y: ef.y * scale,
      ax: er.x,
      ay: er.y,
      dx: down.x,
      dy: down.y,
      depth: ef.z,
    };
  };

  return [build(-1), build(1)];
}
