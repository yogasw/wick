// The same server-rendered data contract feeds Team and Agents.
export type ThemeChoice = { ID: string; Label: string; IsDark: boolean };
export type AccountTheme = { mode: "light" | "dark"; light: string; dark: string };
export type LiveAccountStatus = {
  pid?: number; app_version?: string; wick_version?: string; commit?: string; official?: boolean;
  update_available?: boolean; handing_over?: boolean; draining?: boolean; previous_draining?: boolean;
  swap?: { pending?: boolean; to?: string; blocked?: string };
  auto_swap?: { state?: string; blocking?: string[]; next_check_in?: number };
  last_handover?: { ok?: boolean; error?: string };
};

export function readAccountMenuData(el: HTMLElement | null) {
  const d = el?.dataset ?? {};
  const mode = d.themeMode;
  const theme: AccountTheme | null = mode === "light" || mode === "dark"
    ? { mode, light: d.themeLight ?? "", dark: d.themeDark ?? "" } : null;
  let themes: ThemeChoice[] = [];
  try {
    const parsed = JSON.parse(d.accountThemes || "[]");
    if (Array.isArray(parsed)) themes = parsed;
  } catch { /* No catalog, keep the quick toggle. */ }
  return {
    viewerName: d.viewerName ?? "",
    viewerEmail: d.viewerEmail ?? "",
    viewerAvatar: d.viewerAvatar ?? "",
    isAdmin: d.viewerAdmin !== undefined,
    viewingAs: d.viewingAs ?? "",
    appVersion: d.appVersion ?? "",
    wickVersion: d.wickVersion ?? "",
    buildCommit: d.buildCommit ?? "",
    official: d.officialBuild !== undefined,
    updateAvailable: d.updateAvailable !== undefined,
    servingPID: d.servingPid ?? "",
    theme, themes,
  };
}
