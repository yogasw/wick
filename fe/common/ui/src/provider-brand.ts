// Provider brand from a "type" or "type/name" value — the fixed set that has
// a mark under /public/img/providers. Shared by the composer picker, the
// providers list and the provider detail header so every surface agrees.
export type ProviderBrand = "claude" | "codex" | "gemini" | "opencode" | "omp" | "wick" | "other";

export function providerBrand(value: string): ProviderBrand {
  const t = (value.split("/")[0] || "").toLowerCase();
  if (t === "opencode") return "opencode";
  if (t === "omp") return "omp";
  if (t.includes("claude")) return "claude";
  if (t.includes("codex") || t.includes("openai")) return "codex";
  if (t.includes("gemini")) return "gemini";
  if (t.includes("wick")) return "wick";
  return "other";
}
