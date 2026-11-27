const NEUTRAL = ['default', 'auto'];
// Ordered effort levels only; Default/Auto delegate to the model and sit outside the scale.
function ordered(options) {
  return options.filter(option => !NEUTRAL.includes(option.id));
}
export function effortIndex(options, value) {
  return Math.max(0, options.findIndex(option => option.id === value));
}
/** Position on the effort scale, 0..1, or null when unknown (Default with no known model default). */
export function effortFraction(options, value, defaultValue = '') {
  const id = value === 'default' ? defaultValue : value;
  const list = ordered(options);
  const index = list.findIndex(option => option.id === id);
  if (index < 0) return null;
  return list.length < 2 ? 1 : index / (list.length - 1);
}
/** Needle angle: the gauge spans -120deg (lowest) to +120deg (highest); unknown stays upright. */
export function effortAngle(options, value, defaultValue = '') {
  const f = effortFraction(options, value, defaultValue);
  return f === null ? 0 : -120 + 240 * f;
}
const TONES = ['39 177 153', '59 130 246', '245 158 11', '239 68 68'];
/** RGB triplet for a level: green → blue → amber → red; Default/unknown is slate. */
export function effortTone(options, value, defaultValue = '') {
  if (value === 'default' || NEUTRAL.includes(value)) return '148 163 184';
  const f = effortFraction(options, value, defaultValue);
  if (f === null) return '148 163 184';
  return TONES[Math.min(TONES.length - 1, Math.round(f * (TONES.length - 1)))];
}
