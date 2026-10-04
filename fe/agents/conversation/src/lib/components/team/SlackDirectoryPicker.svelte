<script lang="ts">
  /* Search a Slack workspace for a target instead of typing its id: users
     and bots for a DM, channels for a channel or thread. Picking fills the
     id and the display name (still editable). The id field stays under
     "Enter ID manually" — the fallback when the token lacks the read
     scope or the target is not listed. Only the picked id is stored. */
  import { runApi, searchSlackDirectory, type SlackDirEntry, type SlackIdentity } from "../../api/team.js";

  type Props = {
    base: string;
    connectorId: string;
    identity: SlackIdentity;
    accountId: string;
    kind: "users" | "channels";
    value: string;
    name: string;
    inputId: string;
    idLabel: string;
    idPlaceholder: string;
    idHint?: string;
  };
  let {
    base, connectorId, identity, accountId, kind, value = $bindable(), name = $bindable(),
    inputId, idLabel, idPlaceholder, idHint = "",
  }: Props = $props();

  let query = $state("");
  let entries = $state<SlackDirEntry[]>([]);
  let open = $state(false);
  let loading = $state(false);
  let error = $state("");
  let manual = $state(false);
  let active = $state(0);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let seq = 0;

  function search(q: string) {
    clearTimeout(timer);
    if (!connectorId) return;
    timer = setTimeout(() => {
      const mine = ++seq;
      loading = true;
      runApi(searchSlackDirectory(base, { connectorId, identity, accountId, kind, q }))
        .then((r) => {
          if (mine !== seq) return;
          entries = r.entries ?? [];
          error = r.error ?? "";
          if (r.error) manual = true;
          active = 0;
        })
        .catch((e) => { if (mine === seq) { error = e instanceof Error ? e.message : String(e); manual = true; } })
        .finally(() => { if (mine === seq) loading = false; });
    }, 250);
  }

  function onInput(v: string) {
    query = v;
    open = true;
    search(v);
  }

  function pick(e: SlackDirEntry) {
    value = e.id;
    name = kind === "channels" ? `#${e.name}` : `@${e.display_name || e.name}`;
    query = "";
    open = false;
  }

  function onKey(ev: KeyboardEvent) {
    if (!open || entries.length === 0) return;
    if (ev.key === "ArrowDown") { ev.preventDefault(); active = (active + 1) % entries.length; }
    else if (ev.key === "ArrowUp") { ev.preventDefault(); active = (active - 1 + entries.length) % entries.length; }
    else if (ev.key === "Enter") { ev.preventDefault(); pick(entries[active]); }
    else if (ev.key === "Escape") open = false;
  }

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const hint = "mt-1 text-xs text-black-800 dark:text-black-600";
  const what = $derived(kind === "channels" ? "channels" : "users and bots");
</script>

<div>
  {#if connectorId}
    <label class={label} for="{inputId}-search">Search {what}</label>
    <div class="relative">
      <input
        id="{inputId}-search"
        class={input}
        role="combobox"
        aria-expanded={open && entries.length > 0}
        aria-controls="{inputId}-results"
        aria-autocomplete="list"
        autocomplete="off"
        value={query}
        placeholder={kind === "channels" ? "Type a channel name…" : "Type a name or @handle…"}
        oninput={(e) => onInput((e.currentTarget as HTMLInputElement).value)}
        onfocus={() => { open = true; search(query); }}
        onblur={() => setTimeout(() => (open = false), 150)}
        onkeydown={onKey}
      />
      {#if open && (entries.length > 0 || loading)}
        <ul id="{inputId}-results" role="listbox" class="absolute z-20 mt-1 max-h-64 w-full overflow-auto rounded-lg border border-white-300 bg-white-100 py-1 shadow-lg dark:border-navy-600 dark:bg-navy-800">
          {#if loading && entries.length === 0}<li class="px-3 py-2 text-xs text-black-800 dark:text-black-600">Searching…</li>{/if}
          {#each entries as e, i (e.id)}
            <li
              role="option"
              aria-selected={i === active}
              class="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-sm {i === active ? 'bg-white-200 dark:bg-navy-700' : ''}"
              onmousedown={(ev) => { ev.preventDefault(); pick(e); }}
              onmouseenter={() => (active = i)}
            >
              {#if kind === "users"}
                {#if e.avatar}
                  <img src={e.avatar} alt="" class="h-6 w-6 rounded-md" />
                {:else}
                  <span class="flex h-6 w-6 items-center justify-center rounded-md bg-white-300 text-xs text-black-800 dark:bg-navy-600 dark:text-black-600">{(e.real_name || e.name).slice(0, 1).toUpperCase()}</span>
                {/if}
                <span class="truncate text-black-900 dark:text-white-100">{e.real_name || e.display_name || e.name}</span>
                <span class="truncate text-xs text-black-800 dark:text-black-600">@{e.display_name || e.name}</span>
                {#if e.is_bot}<span class="rounded bg-green-500 px-1.5 text-[10px] font-semibold text-white-100">BOT</span>{/if}
              {:else}
                <span class="text-black-800 dark:text-black-600">{e.is_private ? "🔒" : "#"}</span>
                <span class="truncate text-black-900 dark:text-white-100">{e.name}</span>
              {/if}
              <span class="ml-auto font-mono text-[10px] text-black-800 dark:text-black-600">{e.id}</span>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
    {#if error}
      <p class="mt-1 text-xs text-neg-400" role="alert">{error}</p>
    {:else if value}
      <p class={hint}>Picked <span class="font-mono">{value}</span>{name ? ` · ${name}` : ""}</p>
    {/if}
  {/if}

  <details class="mt-2" open={!connectorId || manual || undefined}>
    <summary class="cursor-pointer text-xs text-black-800 dark:text-black-600">Enter ID manually</summary>
    <div class="mt-2">
      <label class={label} for={inputId}>{idLabel}</label>
      <input id={inputId} class="{input} font-mono" bind:value placeholder={idPlaceholder} />
      {#if idHint}<p class={hint}>{idHint}</p>{/if}
    </div>
  </details>
</div>
