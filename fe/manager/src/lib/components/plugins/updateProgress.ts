import type { PluginProgress } from "$lib/api.js";

/* Human label for a streaming plugin update phase (updatePluginStream).
   Shared by the connector detail kebab/bar and the Admin → Plugins rows. */
export function pluginPhaseLabel(p: PluginProgress | null | undefined): string {
  switch (p?.phase) {
    case "downloading":
      return p.pct >= 0 ? `Downloading… ${p.pct}%` : "Downloading…";
    case "verifying":
      return "Verifying…";
    case "replacing":
      return "Replacing…";
    case "done":
      return "Done";
    default:
      return "Updating…";
  }
}
