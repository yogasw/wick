/* Validates a single file/dir name for the context file tree.
 * Mirrors legacy context.js: trim, then reject empty, slashes, or "..".
 * Nested paths must be created by opening the parent dir first. */
export function isValidFileName(name: string): boolean {
  const trimmed = name.trim();
  if (trimmed === "") return false;
  if (trimmed.includes("/") || trimmed.includes("\\") || trimmed.includes("..")) {
    return false;
  }
  return true;
}
