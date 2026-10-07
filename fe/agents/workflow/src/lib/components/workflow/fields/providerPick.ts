// Bridges the workflow node's provider fields and the shared ProviderPicker.
//
// A node stores the workflow registry's instance NAME in `provider` (that is
// what the engine looks up) and the pinned model in `model`. The picker
// speaks "type/name" or "type/name::modelID". These two helpers convert
// between them using the catalog rows, which carry each instance's type.

export type CatalogProvider = { name: string; type?: string; is_default?: boolean };

/** Picker value for a node's (provider, model). "" = engine default. */
export function toPickValue(providers: CatalogProvider[], provider: string | undefined, model?: string): string {
  const name = (provider ?? "").trim();
  if (!name) return "";
  const row = providers.find((p) => p.name === name);
  // A name the catalog no longer offers (deleted, renamed, or tagged away
  // from this viewer) still shows: buildProviderOptions appends it as
  // "(unavailable)" under its bare key, so a saved node never silently
  // reads as another provider.
  const key = row ? `${row.type || row.name}/${row.name}` : name;
  const m = (model ?? "").trim();
  return m ? `${key}::${m}` : key;
}

/** Splits a picker value back into the node's provider name + model. */
export function fromPickValue(v: string): { provider: string; model: string } {
  const i = v.indexOf("::");
  const key = i < 0 ? v : v.slice(0, i);
  const model = i < 0 ? "" : v.slice(i + 2);
  const s = key.indexOf("/");
  return { provider: s < 0 ? key : key.slice(s + 1), model };
}

/** Catalog rows in the shape buildProviderOptions expects. */
export function catalogProviderList(providers: CatalogProvider[]): { type: string; name: string }[] {
  return providers.map((p) => ({ type: p.type || p.name, name: p.name }));
}
