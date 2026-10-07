<script lang="ts">
  /* The values a plugin remote agent's chat runs with (repository, branch,
     …), as one composer toolbar button that opens a small popover. In a new
     chat each field is editable and "Reset to defaults" clears the
     overrides; once the remote session exists they are locked and the
     popover only shows what the session uses. */
  import type { SessionField } from "../api/team.js";

  type Props = {
    fields: SessionField[];
    values: Record<string, string>;
    locked?: boolean;
    onChange?: (values: Record<string, string>) => void;
  };
  let { fields, values, locked = false, onChange }: Props = $props();

  let open = $state(false);
  let above = $state(true);
  let rootEl: HTMLDivElement | undefined = $state();

  const lockedNote = "Set when the chat started. Start a new chat to change it.";

  function shown(f: SessionField): string {
    return values[f.key] || f.default || "";
  }

  // The button reads "acme/app · release": every field that has a value,
  // so the chat's target is visible without opening anything.
  const summary = $derived(
    fields
      .map((f) => shown(f))
      .filter(Boolean)
      .join(" · "),
  );
  const title = $derived(fields.map((f) => `${f.label}: ${shown(f) || "default"}`).join("\n"));
  const overridden = $derived(fields.some((f) => !!values[f.key]));

  function toggle() {
    if (!open && rootEl) {
      // Open toward the side with more room, like the composer's + menu.
      const r = rootEl.getBoundingClientRect();
      above = r.top > window.innerHeight - r.bottom;
    }
    open = !open;
  }

  function set(key: string, raw: string) {
    const next = { ...values };
    const v = raw.trim();
    if (v) next[key] = v;
    else delete next[key];
    onChange?.(next);
  }

  function reset() {
    onChange?.({});
  }

  function onWindowClick(e: MouseEvent) {
    if (open && rootEl && !rootEl.contains(e.target as Node)) open = false;
  }

  function onWindowKey(e: KeyboardEvent) {
    if (open && e.key === "Escape") open = false;
  }
</script>

<svelte:window onclick={onWindowClick} onkeydown={onWindowKey} />

{#if fields.length > 0}
  <div bind:this={rootEl} class="relative min-w-0" data-session-fields data-locked={locked ? "true" : "false"}>
    <button
      type="button"
      aria-label="Repository and branch"
      aria-expanded={open}
      {title}
      onclick={toggle}
      class="inline-flex h-8 min-w-0 max-w-[16rem] items-center gap-1.5 rounded-lg border border-white-300 bg-white-100 px-2.5 text-xs text-black-900 transition-colors hover:bg-white-200 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100 dark:hover:bg-navy-600"
      data-session-fields-button
    >
      {#if locked}
        <svg aria-hidden="true" class="h-3.5 w-3.5 shrink-0 text-black-600 dark:text-black-600" viewBox="0 0 16 16" fill="currentColor"><path d="M8 1a3 3 0 0 0-3 3v2H4a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V7a1 1 0 0 0-1-1h-1V4a3 3 0 0 0-3-3Zm-1.5 5V4a1.5 1.5 0 1 1 3 0v2h-3Z" /></svg>
      {:else}
        <svg aria-hidden="true" class="h-3.5 w-3.5 shrink-0 text-black-600 dark:text-black-600" viewBox="0 0 16 16" fill="currentColor"><path d="M5 3.25a.75.75 0 1 1-1.5 0 .75.75 0 0 1 1.5 0Zm0 2.122a2.25 2.25 0 1 0-1.5 0v5.256a2.25 2.25 0 1 0 1.5 0V9.25c.47.35 1.05.5 1.75.5h2.5A2.25 2.25 0 0 0 11.5 7.5v-.128a2.25 2.25 0 1 0-1.5 0v.128a.75.75 0 0 1-.75.75h-2.5A1.75 1.75 0 0 1 5 6.5V5.372ZM4.25 12a.75.75 0 1 1 0 1.5.75.75 0 0 1 0-1.5Zm6.5-6.5a.75.75 0 1 1 0-1.5.75.75 0 0 1 0 1.5Z" /></svg>
      {/if}
      <span class="min-w-0 truncate">{summary || fields[0].placeholder || fields[0].label}</span>
    </button>

    {#if open}
      <div
        class="absolute left-0 z-30 w-72 max-w-[calc(100vw-2rem)] rounded-xl border border-white-300 bg-white-100 p-3 shadow-lg dark:border-navy-600 dark:bg-navy-800 {above ? 'bottom-full mb-2' : 'top-full mt-2'}"
        data-session-fields-popover
      >
        <div class="mb-1 text-xs font-semibold text-black-900 dark:text-white-100">
          {fields.map((f) => f.label).join(" & ")}
        </div>
        {#each fields as f (f.key)}
          <label class="mt-2 block text-[11px] text-black-700 dark:text-black-600" for={`sf-${f.key}`}>{f.label}</label>
          {#if locked}
            <div
              id={`sf-${f.key}`}
              class="mt-1 truncate rounded-lg border border-white-300 bg-white-200/60 px-2 py-1.5 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
              data-session-field={f.key}
            >
              {shown(f) || "default"}
            </div>
          {:else}
            <input
              id={`sf-${f.key}`}
              type="text"
              aria-label={f.label}
              value={values[f.key] ?? ""}
              placeholder={f.default || f.placeholder || f.label}
              onchange={(e) => set(f.key, (e.currentTarget as HTMLInputElement).value)}
              onkeydown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  set(f.key, (e.currentTarget as HTMLInputElement).value);
                }
              }}
              class="mt-1 w-full rounded-lg border border-white-300 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
              data-session-field-input={f.key}
            />
          {/if}
        {/each}
        {#if locked}
          <p class="mt-3 text-[11px] text-black-700 dark:text-black-600">{lockedNote}</p>
        {:else if overridden}
          <button
            type="button"
            class="mt-3 text-[11px] text-black-700 hover:text-black-900 dark:text-black-600 dark:hover:text-white-100"
            onclick={reset}>Reset to defaults</button
          >
        {/if}
      </div>
    {/if}
  </div>
{/if}
