<script lang="ts">
  // Key-value list editor used by http headers/query, shell env, channel
  // and connector args. Each value supports the Fixed ⇄ Expression mode
  // pill via ArgField. This is a thin wrapper over the shared common-ui
  // KvList: KvList owns the row container + remove + list iteration; this
  // wrapper keeps the map storage, the per-key mode reconciliation, and the
  // add-staging row (a map can't hold an empty-key row, so adds stage here).
  //
  // Storage shape: { [key: string]: string }. The parent provides the
  // current map + the matching mode map (Record<string, "fixed" |
  // "expression">) so this component stays stateless across renders.

  import { KvList } from "@wick-fe/common-ui";
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

  const rows = $derived(
    Object.entries(entries ?? {}).map(([key, value]) => ({ key, value })),
  );

  const keyInputClass =
    "rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 font-mono text-[12px] flex-1";

  function setValue(k: string, v: string) {
    const next = { ...(entries ?? {}) };
    next[k] = v;
    onChange(next);
  }

  function renameKey(oldKey: string, nextKey: string) {
    if (!nextKey || nextKey === oldKey) return;
    const next: Record<string, string> = {};
    for (const [k, v] of Object.entries(entries ?? {})) {
      next[k === oldKey ? nextKey : k] = v;
    }
    onChange(next);
    if (modes && onModeChange) {
      const m: Record<string, string> = {};
      for (const [k, v] of Object.entries(modes)) {
        m[k === oldKey ? nextKey : k] = v;
      }
      onModeChange(m);
    }
  }

  function setMode(k: string, m: Mode) {
    if (!onModeChange) return;
    const next = { ...(modes ?? {}) };
    next[k] = m;
    onModeChange(next);
  }

  // KvList fires onChange only on row removal here (key/value edits go through
  // the wrapper's own handlers via the row snippet). Rebuild the map + modes
  // from the surviving rows.
  function handleRows(next: Record<string, string>[]) {
    const map: Record<string, string> = {};
    for (const r of next) {
      map[r.key] = r.value;
    }
    onChange(map);
    if (modes && onModeChange) {
      const m: Record<string, string> = {};
      for (const r of next) {
        if (modes[r.key] !== undefined) {
          m[r.key] = modes[r.key];
        }
      }
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
</script>

<div class="space-y-2">
  <KvList
    {label}
    {helper}
    columns={["key", "value"]}
    rows={rows}
    showAdd={false}
    onChange={handleRows}
  >
    {#snippet row({ row: entry, remove })}
      <div class="flex items-center gap-2">
        <input
          class={keyInputClass}
          placeholder={keyPlaceholder}
          value={entry.key}
          onchange={(e) => renameKey(entry.key, (e.target as HTMLInputElement).value)}
        />
        <button
          type="button"
          class="text-rose-500 text-xs px-2"
          onclick={remove}
          title="Remove entry"
        >✕</button>
      </div>
      <ArgField
        label="value"
        value={entry.value}
        mode={(modes?.[entry.key] as Mode | undefined) ?? "fixed"}
        placeholder={valuePlaceholder}
        onValueChange={(nv) => setValue(entry.key, nv)}
        onModeChange={onModeChange ? (nm) => setMode(entry.key, nm) : undefined}
      />
    {/snippet}
  </KvList>
  <div class="flex items-center gap-2 pt-1">
    <input
      class={keyInputClass}
      placeholder={keyPlaceholder}
      bind:value={newKey}
      onkeydown={(e) => e.key === "Enter" && add()}
    />
    <input
      class={keyInputClass}
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
