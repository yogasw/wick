export type Rgb = { r: number; g: number; b: number };

export function parseHex(color: string): Rgb {
  const hex = color.replace("#", "").trim();
  const full = hex.length === 3 ? hex.split("").map((c) => c + c).join("") : hex;
  const value = Number.parseInt(full.slice(0, 6), 16);
  if (Number.isNaN(value)) return { r: 79, g: 142, b: 247 };
  return {
    r: (value >> 16) & 255,
    g: (value >> 8) & 255,
    b: value & 255,
  };
}

export function formatHex(rgb: Rgb): string {
  const to = (n: number) => Math.round(n).toString(16).padStart(2, "0");
  return `#${to(rgb.r)}${to(rgb.g)}${to(rgb.b)}`;
}

export function luminance(rgb: Rgb): number {
  return (0.2126 * rgb.r + 0.7152 * rgb.g + 0.0722 * rgb.b) / 255;
}

export function contrastInk(color: string): string {
  return luminance(parseHex(color)) > 0.62 ? "#111111" : "#ffffff";
}

export function lerpRgb(from: Rgb, to: Rgb, t: number): Rgb {
  return {
    r: from.r + (to.r - from.r) * t,
    g: from.g + (to.g - from.g) * t,
    b: from.b + (to.b - from.b) * t,
  };
}
