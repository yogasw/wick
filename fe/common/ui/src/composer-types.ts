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

/* A themed dropdown in the Composer toolbar (provider / project / preset).
   `badge` is an optional short marker shown as a pill next to an option (and as
   a corner dot on the chip when that option is selected) — e.g. "AI Router". */
export type ComposerSelect = {
  options: { label: string; value: string; badge?: string }[];
  value: string;
  onChange: (v: string) => void;
};
