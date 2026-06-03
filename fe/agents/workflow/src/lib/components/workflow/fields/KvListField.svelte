<script lang="ts">
  // Key-value list editor used by http headers/query, shell env, channel
  // and connector args — same surface as the legacy editor's kvlist
  // rows in node module ArgForms. Each value supports the Fixed ⇄
  // Expression mode pill so callers can template individual entries.
  //
  // Storage shape: { [key: string]: string }. The parent provides the
  // current map + the matching mode map (Record<string, "fixed" |
  // "expression">) so this component stays stateless across renders.

  import ArgField from "./ArgField.svelte";

  type Mode = "fixed" | "expression";
  type Props = {
    label: string;
    entries: Record<string, string> | undefined;
    modes?: Record<string, string>;
    helper?: string;
    keyPlaceholder?: string;
    valuePlaceholder?: string;
    onChange: (next: Record<string, string>) => void;
    onModeChange?: (modes: Record<string, string>) => void;
  };

  let {
    label,
    entries,
    modes,
    helper,
    keyPlaceholder = "key",
    valuePlaceholder = "value",
    onChange,
    onModeChange,
  }: Props = $props();

  let newKey = $state("");
  let newValue = $state("");

  function setValue(k: string, v: string) {
    const next = { ...(entries ?? {}) };
    next[k] = v;
    onChange(next);
  }

  function renameKey(oldKey: string, newKey: string) {
    if (!newKey || newKey === oldKey) return;
    const next: Record<string, string> = {};
    for (const [k, v] of Object.entries(entries ?? {})) {
      next[k === oldKey ? newKey : k] = v;
    }
    onChange(next);
    if (modes && onModeChange) {
      const m: Record<string, string> = {};
      for (const [k, v] of Object.entries(modes)) {
        m[k === oldKey ? newKey : k] = v;
      }
      onModeChange(m);
    }
  }

  function remove(k: string) {
    const next = { ...(entries ?? {}) };
    delete next[k];
    onChange(next);
    if (modes && onModeChange) {
      const m = { ...modes };
      delete m[k];
      onModeChange(m);
    }
  }

  function add() {
    const k = newKey.trim();
    if (!k) return;
    setValue(k, newValue);
    newKey = "";
    newValue = "";
  }

  function setMode(k: string, m: Mode) {
    if (!onModeChange) return;
    const next = { ...(modes ?? {}) };
    next[k] = m;
    onModeChange(next);
  }
</script>

<div class="space-y-2">
  <div class="flex items-center justify-between">
    <span class="text-xs font-medium">{label}</span>
  </div>
  {#if helper}
    <span class="text-[11px] text-black-700 dark:text-black-600">{helper}</span>
  {/if}
  {#each Object.entries(entries ?? {}) as [k, v] (k)}
    <div class="rounded border border-slate-200 dark:border-navy-600 p-2 space-y-1">
      <div class="flex items-center gap-2">
        <input
          class="rounded border border-slate-200 dark:border-navy-600 bg-white dark:bg-navy-700 px-2 py-1 font-mono text-[12px] flex-1"
          value={k}
          onchange={(e) => renameKey(k, (e.target as HTMLInputElement).value)}
        />
        <button
          type="button"
          class="text-rose-500 text-xs px-2"
          onclick={() => remove(k)}
          title="Remove entry"
        >✕</button>
      </div>
      <ArgField
        label="value"
        value={v}
        mode={(modes?.[k] as Mode | undefined) ?? "fixed"}
        placeholder={valuePlaceholder}
        onValueChange={(nv) => setValue(k, nv)}
        onModeChange={onModeChange ? (nm) => setMode(k, nm) : undefined}
      />
    </div>
  {/each}
  <div class="flex items-center gap-2 pt-1">
    <input
      class="rounded border border-slate-200 dark:border-navy-600 bg-white dark:bg-navy-700 px-2 py-1 font-mono text-[12px] flex-1"
      placeholder={keyPlaceholder}
      bind:value={newKey}
      onkeydown={(e) => e.key === "Enter" && add()}
    />
    <input
      class="rounded border border-slate-200 dark:border-navy-600 bg-white dark:bg-navy-700 px-2 py-1 font-mono text-[12px] flex-1"
      placeholder={valuePlaceholder}
      bind:value={newValue}
      onkeydown={(e) => e.key === "Enter" && add()}
    />
    <button
      type="button"
      class="text-emerald-600 text-xs px-2"
      onclick={add}
      title="Add entry"
    >+</button>
  </div>
</div>
