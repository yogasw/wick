/* One entry in the Composer's `/` command menu (built-in actions + skills).
   `label` is what the menu shows (e.g. "/reset"); `hint` is an optional
   right-aligned note; `category` groups rows under a header. Either `run` (an
   action) OR text insertion (`value` placed after the `/`, used by skills)
   fires on select. */
export type ComposerCommand = {
  value: string;
  label: string;
  hint?: string;
  category?: string;
  run?: () => void;
};

import type { ModelCaps } from "./capability-types.js";

/* One selectable model nested under a provider option (see ComposerSelectOption.models). */
export type ComposerModelOption = {
  id: string;
  label: string;
  default: boolean;
  /** Short human description shown under the model name in the picker. */
  desc?: string;
  /** True when this row is a LIVE MODEL SET, not a single model: the picker
      shows it as an expandable row (a 4th level) resolved by this row's `id`
      via loadModels({entry: id}). The vendor filter stays server-side. */
  live?: boolean;
  /** Vendor-reported capabilities for this model (raw). Present only for rows
      that came from live discovery (live-set expansion); undefined otherwise. */
  caps?: ModelCaps;
};

/* A themed dropdown in the Composer toolbar (provider / project / preset).
   `badge` is an optional short marker shown as a pill next to an option (and as
   a corner dot on the chip when that option is selected) — e.g. "AI Router".
   `models`, when present with >1 entry, adds a 3rd picker level: selecting this
   option first drills into its model list instead of committing immediately. */
export type ComposerSelectOption = {
  label: string;
  value: string;
  badge?: string;
  models?: ComposerModelOption[];
};

export type ComposerSelect = {
  options: ComposerSelectOption[];
  value: string;
  onChange: (v: string) => void;
  /* Optional lazy loader for a provider's model list. When present, drilling
     into an option calls this with the option's value; the returned models
     replace that option's static `models` for the drill (cached per value).
     On error or empty result the picker keeps the static list. Lets the
     composer show the vendor's live models without prefetching every
     provider up front.

     For a 4th level (opening a LIVE SET row), it's called again with `opts`
     identifying the set — `entry` (the set's model id) — and returns that
     set's expanded models. */
  loadModels?: (
    optionValue: string,
    opts?: { entry?: string },
  ) => Promise<ComposerModelOption[]>;
  /** Render capability chips on model rows. Default true (undefined = show). */
  showCapabilities?: boolean;
  /** Chip display mode when shown. Default "icon". */
  capabilityMode?: import("./capability-types.js").CapabilityDisplayMode;
};
