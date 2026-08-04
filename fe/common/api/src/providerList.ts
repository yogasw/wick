/* The provider instances a picker may offer, as the server reports them.
   Shared because three surfaces render the same list — the composer, the
   project defaults, and the sub-agent role editor — and a fourth copy of
   these shapes would be a fourth chance to drift. */

/** One model choice under a provider instance. */
export interface ProviderModelItem {
  id: string;
  label: string;
  default: boolean;
  desc?: string;
  /** True when this row is a live model SET rather than a single model: it
      expands one level further (resolved server-side by this row's id) and is
      not selectable itself. Its leaves are pinned as "<id>@<vendorModelID>". */
  live?: boolean;
}

/** One selectable provider instance. `type/name` is the stored key. */
export interface ProviderListItem {
  type: string;
  name: string;
  models?: ProviderModelItem[];
}
