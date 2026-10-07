/* accounts.ts — client-side helpers for the provider types where one
   instance = one account (omp, opencode). Mirrors the BE rules in
   internal/agents/provider/accounts.go so the add form can preview what
   the server will pin; the server stays the authority. */

export const ACCOUNT_ISOLATED = new Set(["omp", "opencode"]);

/* What one instance holds for an account-isolated type — the hint behind
   the card's info icon. omp pools several accounts in one profile and
   opencode takes extra accounts as extra folders, so "1 instance = 1
   account" is no longer the whole story. */
export const ACCOUNT_HINT: Record<string, string> = {
  omp: "One instance = one omp profile. It can hold several accounts; omp rotates between them.",
  opencode: "One instance = one data folder. Add a second account of a provider as an extra account folder.",
};

export function accountHint(t: string): string {
  return ACCOUNT_HINT[t] ?? "";
}

export type TypeInfo = { label: string; desc: string; badge?: string };

/* Short description per type for the Add provider picker. Unknown types
   fall back to the bare key. */
export const TYPE_INFO: Record<string, TypeInfo> = {
  claude: { label: "Claude Code", desc: "Anthropic claude CLI" },
  codex: { label: "Codex", desc: "OpenAI codex CLI" },
  gemini: { label: "Gemini CLI", desc: "Google gemini CLI", badge: "experimental" },
  omp: { label: "oh-my-pi (omp)", desc: "Login ChatGPT atau Claude · 1 profile per instance" },
  opencode: { label: "opencode", desc: "Data dir sendiri per instance · Claude subscription tidak didukung" },
  wick: { label: "Wick", desc: "Built-in engine" },
};

export function typeLabel(t: string): string {
  const i = TYPE_INFO[t];
  return i ? `${i.label} — ${i.desc}` : t;
}

/* typeOption is the <Select> entry for a provider type: short label on
   the first line, description underneath, optional badge. Unknown types
   fall back to the bare key. */
export function typeOption(t: string): { label: string; value: string; description?: string; badge?: string } {
  const i = TYPE_INFO[t];
  if (!i) return { label: t, value: t };
  return { label: i.label, value: t, description: i.desc, ...(i.badge ? { badge: i.badge } : {}) };
}

/* suggestName picks `type`, then `type_2`, `type_3`, … — the first not
   taken. '_' because instance names only allow [A-Za-z0-9_]. */
export function suggestName(type: string, taken: string[]): string {
  const used = new Set(taken);
  if (!used.has(type)) return type;
  for (let i = 2; i < 1000; i++) {
    const n = `${type}_${i}`;
    if (!used.has(n)) return n;
  }
  return "";
}

/* defaultOMPProfile mirrors provider.DefaultOMPProfile: "wick-<name>" in
   omp's accepted alphabet ^[a-z0-9][a-z0-9._-]{0,63}$. */
export function defaultOMPProfile(name: string): string {
  let n = name.trim().toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^[-.]+|[-.]+$/g, "");
  if (!n) n = "default";
  let p = `wick-${n}`;
  if (p.length > 64) p = p.slice(0, 64).replace(/[-.]+$/, "");
  return p;
}

export function validOMPProfile(p: string): boolean {
  return /^[a-z0-9][a-z0-9._-]{0,63}$/.test(p);
}

/* accountStorePreview is what the form shows before the instance exists. */
export function accountStorePreview(type: string, name: string): string {
  if (type === "omp") return `profile ${defaultOMPProfile(name || "omp")}`;
  if (type === "opencode") return `<wick data>/providers/opencode/${name || "opencode"}`;
  return "";
}

/* sourceLabel names where an instance's binary comes from, for the card. */
export function sourceLabel(source: string | undefined): string {
  switch (source) {
    case "managed":
      return "managed by wick";
    case "registry":
      return "manual path";
    case "path":
      return "PATH";
    case "scan":
      return "found on disk";
    case "miss":
      return "not installed";
  }
  return "";
}
