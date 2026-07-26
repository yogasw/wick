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

/* One selectable model nested under a provider option (see ComposerSelectOption.models). */
export type ComposerModelOption = {
  id: string;
  label: string;
  default: boolean;
  /** Short human description shown under the model name in the picker. */
  desc?: string;
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
};
