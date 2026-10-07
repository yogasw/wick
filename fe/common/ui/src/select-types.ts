/** One <Select> entry. A bare string is both label and value.
    `description` is a muted second line (wraps to 2 lines), `badge` a
    small chip on the right, `disabled` greys it out and keyboard
    navigation skips it. */
export type SelectOption =
  | string
  | { label: string; value: string; description?: string; badge?: string; disabled?: boolean };
