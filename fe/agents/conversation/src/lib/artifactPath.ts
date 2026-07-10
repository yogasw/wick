/* Path validation for the artifact file bridge (window.wickReadFile). A
   sandboxed HTML artifact can ask the parent to read a session file; the parent
   is the gatekeeper and must only ever serve paths INSIDE the session dir.
   safeReadPath returns a normalised relative path, or null if the input could
   escape the session (absolute, protocol-qualified, or `..` traversal). */
export function safeReadPath(p: unknown): string | null {
  if (typeof p !== "string" || !p.trim()) return null;
  const s = p.trim().replace(/\\/g, "/");
  if (/^[a-z][a-z0-9+.-]*:/i.test(s)) return null; // http:, file:, data:, …
  if (s.startsWith("/") || /^[a-z]:/i.test(s)) return null; // absolute (posix / windows drive)
  if (s.split("/").some((seg) => seg === "..")) return null; // parent-dir traversal
  return s;
}
