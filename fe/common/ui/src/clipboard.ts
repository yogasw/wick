/* Copy text to the clipboard from anywhere in the app.
 *
 * `navigator.clipboard` only exists in a SECURE CONTEXT — https, or
 * localhost. wick is routinely reached over plain http on a LAN address or an
 * internal hostname, where the whole API is simply undefined, so a component
 * that calls it directly does nothing at all on exactly the hosts people use
 * it from. The execCommand path is deprecated and still the only thing that
 * works there, so it stays as the fallback rather than as a legacy branch
 * somebody will tidy away.
 */
export async function copyText(text: string): Promise<boolean> {
  if (!text) return false;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Permission denied, or a browser that has the API but refuses it
    // without a user gesture it recognised. Fall through.
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    // Off-screen rather than display:none — a hidden element cannot be
    // selected, and an unselected one copies nothing.
    ta.style.position = "fixed";
    ta.style.top = "-1000px";
    ta.style.opacity = "0";
    ta.setAttribute("readonly", "");
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    ta.remove();
    return ok;
  } catch {
    return false;
  }
}
