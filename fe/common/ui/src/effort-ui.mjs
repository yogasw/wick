export function effortIndex(options, value) {
  return Math.max(0, options.findIndex(option => option.id === value));
}
export function effortAngle(options, value, defaultValue = '') {
  const id = value === 'default' ? defaultValue : value;
  const ordered = options.filter(option => !['default', 'auto'].includes(option.id));
  const index = ordered.findIndex(option => option.id === id);
  return index < 0 || ordered.length < 2 ? 0 : -70 + 140 * index / (ordered.length - 1);
}
