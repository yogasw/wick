<script lang="ts">
  // Field — one-stop input row used by every node form in the
  // inspector. Bundles label + helper + error + optional
  // Fixed ⇄ Expression toggle so callers stop reimplementing the
  // same `<label><span>…</span><input … /></label>` markup over and
  // over.
  //
  // Switch shape via `kind`:
  //   text     — single-line input
  //   textarea — multiline input (auto-grow via `rows`)
  //   number   — numeric input, value coerced through Number()
  //   select   — dropdown; pass `options` (string[] or {label, value}[])
  //   checkbox — boolean toggle
  //   list     — string[] edited as one-per-line textarea
  //
  // `expression: true` adds the Fixed/Expression mode pill for
  // template-aware text/textarea fields. Caller manages where
  // mode lives (typically node.arg_modes[key]).
  import ArgField from "./ArgField.svelte";

  type Mode = "fixed" | "expression";
  type SelectOption = string | { label: string; value: string };

  type Props = {
    kind?: "text" | "textarea" | "number" | "select" | "checkbox" | "list";
    label: string;
    value: unknown;
    onChange: (v: any) => void;

    // common
    helper?: string;
    error?: string;
    placeholder?: string;
    required?: boolean;
    disabled?: boolean;

    // textarea / list
    rows?: number;

    // select
    options?: SelectOption[];

    // template mode pill — shown for every kind when `expression` is on.
    // text/textarea defer to ArgField (with live preview); other kinds get
    // a lightweight pill above the native input.
    expression?: boolean;
    mode?: Mode;
    onModeChange?: (m: Mode) => void;
    // lockedMode greys out + disables the pill (mode pinned by config tag).
    lockedMode?: boolean;
  };

  let {
    kind = "text",
    label,
    value,
    onChange,
    helper,
    error,
    placeholder,
    required = false,
    disabled = false,
    rows = 4,
    options = [],
    expression = false,
    mode = "fixed",
    onModeChange,
    lockedMode = false,
  }: Props = $props();

  // Pill shows for non-text kinds only here — text/textarea route through
  // ArgField which renders its own pill (+ preview). Gated on having an
  // onModeChange handler so read-only call sites stay clean.
  const showInlinePill = $derived(
    expression && !!onModeChange && kind !== "text" && kind !== "textarea",
  );

  const baseInput =
    "rounded border bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm";
  // Error wins the colour, then required-empty (amber), else slate.
  function borderClass(hasError: boolean): string {
    if (hasError) {
      return "border-rose-500";
    }
    return "border-slate-200 dark:border-navy-600";
  }

  function isObjOption(o: SelectOption): o is { label: string; value: string } {
    return typeof o === "object" && o !== null;
  }
</script>

<!-- Shared Fixed/Expression pill — one definition reused by every kind
     so the toggle reads identically across the form. -->
{#snippet modePill()}
  <div
    class="inline-flex rounded border border-slate-300 dark:border-navy-600 overflow-hidden text-[10px] uppercase tracking-wide"
    class:opacity-50={lockedMode}
    title={lockedMode ? "Mode locked by this field's config — cannot change" : undefined}
  >
    {#each (["fixed", "expression"] as const) as m}
      <button
        type="button"
        class="px-2 py-0.5 transition-colors"
        class:bg-emerald-500={mode === m}
        class:text-white-100={mode === m}
        class:text-black-700={mode !== m}
        class:text-black-600={mode !== m}
        class:cursor-not-allowed={lockedMode}
        disabled={lockedMode}
        onclick={() => { if (!lockedMode) onModeChange?.(m); }}
        title={m === "fixed"
          ? "Literal value — template NOT rendered (verbatim output)"
          : "Go template — evaluated at runtime"}
      >{m}</button>
    {/each}
  </div>
{/snippet}

<div class="space-y-1">
  {#if kind === "checkbox"}
    <!-- Checkbox flips the layout — label sits next to the box, not
         above. Still rendered through the same Field wrapper so all
         node inputs read the same shape at call sites. In expression
         mode the box is replaced by a text input so the operator can
         type a template that renders to a bool at runtime. -->
    <div class="flex items-center justify-between gap-2">
      <label class="inline-flex items-center gap-2 cursor-pointer">
        {#if mode === "expression" && showInlinePill}
          <input
            class="{baseInput} w-40 font-mono {borderClass(!!error)}"
            type="text"
            placeholder="{'{{...}}'}"
            {disabled}
            value={typeof value === "string" ? value : String(value ?? "")}
            oninput={(e) => onChange((e.target as HTMLInputElement).value)}
          />
        {:else}
          <input
            type="checkbox"
            class="w-4 h-4 accent-emerald-500 cursor-pointer"
            checked={!!value}
            {disabled}
            onchange={(e) => onChange((e.target as HTMLInputElement).checked)}
          />
        {/if}
        <span class="text-xs font-medium">{label}{#if required}<span class="text-rose-500"> *</span>{/if}</span>
      </label>
      {#if showInlinePill}{@render modePill()}{/if}
    </div>
  {:else if expression && (kind === "text" || kind === "textarea")}
    <!-- Templatable text — defer to the existing ArgField primitive.
         Keeps the Fixed/Expression pill consistent everywhere. -->
    <ArgField
      {label}
      value={typeof value === "string" ? value : String(value ?? "")}
      {mode}
      {lockedMode}
      multiline={kind === "textarea"}
      {rows}
      {placeholder}
      helper={error ?? helper}
      onValueChange={(v) => onChange(v)}
      {onModeChange}
    />
  {:else}
    <div class="flex items-center justify-between gap-2">
      <span class="block text-xs font-medium">
        {label}{#if required}<span class="text-rose-500"> *</span>{/if}
      </span>
      {#if showInlinePill}{@render modePill()}{/if}
    </div>

    {#if kind === "text"}
      <input
        class="{baseInput} w-full {borderClass(!!error)}"
        type="text"
        {placeholder}
        {disabled}
        value={typeof value === "string" ? value : String(value ?? "")}
        oninput={(e) => onChange((e.target as HTMLInputElement).value)}
      />
    {:else if kind === "textarea"}
      <textarea
        class="{baseInput} w-full font-mono {borderClass(!!error)}"
        {rows}
        {placeholder}
        {disabled}
        value={typeof value === "string" ? value : String(value ?? "")}
        oninput={(e) => onChange((e.target as HTMLTextAreaElement).value)}
      ></textarea>
    {:else if kind === "number"}
      <input
        class="{baseInput} w-full {borderClass(!!error)}"
        type="number"
        {placeholder}
        {disabled}
        value={value as number | string | undefined}
        oninput={(e) => {
          const raw = (e.target as HTMLInputElement).value;
          onChange(raw === "" ? 0 : Number(raw));
        }}
      />
    {:else if kind === "select"}
      <select
        class="{baseInput} w-full {borderClass(!!error)}"
        {disabled}
        value={typeof value === "string" ? value : String(value ?? "")}
        onchange={(e) => onChange((e.target as HTMLSelectElement).value)}
      >
        {#each options as o}
          {#if isObjOption(o)}
            <option value={o.value}>{o.label}</option>
          {:else}
            <option value={o}>{o}</option>
          {/if}
        {/each}
      </select>
    {:else if kind === "list"}
      <textarea
        class="{baseInput} w-full font-mono {borderClass(!!error)}"
        {rows}
        {placeholder}
        {disabled}
        value={Array.isArray(value) ? value.join("\n") : ""}
        oninput={(e) =>
          onChange(
            (e.target as HTMLTextAreaElement).value.split(/\r?\n/).filter(Boolean),
          )}
      ></textarea>
    {/if}

    {#if error}
      <span class="text-[11px] text-rose-600 dark:text-rose-400">{error}</span>
    {:else if helper}
      <span class="text-[11px] text-black-700 dark:text-black-600">{helper}</span>
    {/if}
  {/if}
</div>
