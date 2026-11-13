<!--
  AccessRoles — the "Akses & peran" tab: one screen for "who may use what,
  running as whom". A mode is a shortcut that writes the grants the agent
  already stores; the table edits them per connector. Opening the tab
  changes nothing: edits stay local until the owner presses Simpan, after
  a summary of what will change. Per-person tiers the server does not
  have yet are shown disabled as "segera".
-->
<script lang="ts">
  import { untrack } from "svelte";
  import type { AgentConnector, ConnectorGrant, EffectiveRow, AccessTrailItem, ShareUser } from "../api/team.js";
  import { getAccessEffective, getAccessTrail, listShareUsers, runApi } from "../api/team.js";
  import {
    PRESETS, accessDiff, applyPreset, cellFor, detectPreset, levelName, setRow,
    type AccessMode, type AccessPreset,
  } from "../accessRoles.js";

  type Props = {
    base: string;
    agentId: string;
    catalog: AgentConnector[];
    savedGrants: ConnectorGrant[];
    savedMode: AccessMode;
    onApply: (grants: ConnectorGrant[], mode: AccessMode) => void;
  };
  let { base, agentId, catalog, savedGrants, savedMode, onApply }: Props = $props();

  let view = $state<"roles" | "raw">("roles");
  let grants = $state<ConnectorGrant[]>(untrack(() => structuredClone(savedGrants)));
  let mode = $state<AccessMode>(untrack(() => savedMode));
  let confirming = $state(false);

  const rows = $derived(catalog.filter((c) => !c.tool && (c.tier ?? "") === ""));
  const labelOf = (id: string) => catalog.find((c) => c.id === id)?.label ?? id;
  const grantOf = (id: string) => grants.find((g) => g.connector_id === id);
  const preset = $derived(detectPreset(mode, grants));
  const diff = $derived(accessDiff({ mode: savedMode, grants: savedGrants }, { mode, grants }, labelOf));

  function pick(p: AccessPreset) {
    const r = applyPreset(p, mode, grants);
    mode = r.mode;
    grants = r.grants;
  }
  function editRow(id: string, level: ConnectorGrant["level"], lend: boolean) {
    grants = setRow(grants, id, level, lend);
  }
  function reset() {
    grants = structuredClone(savedGrants);
    mode = savedMode;
    confirming = false;
  }
  function save() {
    onApply(structuredClone($state.snapshot(grants)) as ConnectorGrant[], mode);
    confirming = false;
  }

  /* Preview and trail read the SAVED access from the server. */
  let previewAs = $state("owner");
  let users = $state<ShareUser[]>([]);
  let preview = $state<EffectiveRow[] | null>(null);
  let previewError = $state("");
  let trail = $state<AccessTrailItem[] | null>(null);
  $effect(() => {
    void runApi(listShareUsers(base)).then((r) => (users = r.users ?? [])).catch(() => (users = []));
    void runApi(getAccessTrail(base, agentId)).then((r) => (trail = r.items ?? [])).catch(() => (trail = []));
  });
  $effect(() => {
    const who = previewAs;
    const [as, uid] = who.startsWith("user:") ? (["user", who.slice(5)] as const) : ([who as "owner" | "guest", ""] as const);
    previewError = "";
    runApi(getAccessEffective(base, agentId, as, uid))
      .then((r) => (preview = r.items ?? []))
      .catch((e) => {
        preview = [];
        previewError = e instanceof Error ? e.message : String(e);
      });
  });

  const lentStatus = (r: EffectiveRow | undefined) => {
    if (!r || !r.lent) return { text: "—", cls: "text-black-700 dark:text-black-600" };
    switch (r.account_status) {
      case "connected": return { text: "tersambung", cls: "text-green-600 dark:text-green-400" };
      case "other_user": return { text: "milik user lain", cls: "text-neg-400" };
      case "missing": return { text: "belum tersambung", cls: "text-cau-500" };
    }
    return { text: "akun pilihan pemanggil", cls: "text-black-800 dark:text-black-600" };
  };
  let ownerView = $state<EffectiveRow[]>([]);
  $effect(() => {
    void runApi(getAccessEffective(base, agentId, "guest")).then((r) => (ownerView = r.items ?? [])).catch(() => (ownerView = []));
  });

  const card = "rounded-xl border border-white-300 bg-white-100 p-4 dark:border-navy-600 dark:bg-navy-700";
  const hint = "text-xs text-black-800 dark:text-black-600";
  const pill = "inline-block rounded-full border px-2 py-0.5 text-xs";
  const lvCls = (lv: string) =>
    lv === "off" ? "border-white-300 text-black-700 dark:border-navy-600 dark:text-black-600"
    : lv === "read" ? "border-sky-700 text-sky-500 dark:text-sky-300"
    : "border-green-700 text-green-600 dark:text-green-400";
  const select = "rounded-lg border border-white-300 bg-white-100 px-2 py-1 text-xs dark:border-navy-600 dark:bg-navy-800";
</script>

<div class="space-y-4" data-testid="access-roles">
  <div class="flex gap-1.5 border-b border-white-300 dark:border-navy-600" role="tablist">
    {#each [["roles", "Akses & peran"], ["raw", "Raw"]] as [id, name] (id)}
      <button type="button" role="tab" aria-selected={view === id} data-testid={`access-roles-tab-${id}`}
        class="rounded-t-lg px-3 py-1.5 text-sm {view === id ? 'border border-b-0 border-white-300 bg-white-100 text-black-900 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100' : 'text-black-800 dark:text-black-600'}"
        onclick={() => (view = id as "roles" | "raw")}>{name}</button>
    {/each}
  </div>

  {#if view === "raw"}
    <section class={card} data-testid="access-raw">
      <p class="text-sm font-semibold text-black-900 dark:text-white-100">Grants tersimpan (read-only)</p>
      <p class={hint}>Persis yang tersimpan di agent ini; yang dipakai saat agent berjalan.</p>
      <pre class="mt-2 overflow-auto rounded-lg border border-white-300 p-2 text-xs dark:border-navy-600 dark:bg-navy-800">{JSON.stringify({ access_mode: savedMode, grants: savedGrants }, null, 2)}</pre>
    </section>
  {:else}
    <section class={card}>
      <p class="text-sm font-semibold text-black-900 dark:text-white-100">1. Pilih mode</p>
      <p class={hint}>Mode hanya jalan pintas ke pengaturan di tabel bawah. Pengecualian per connector diatur di tabel.</p>
      <div class="mt-3 grid grid-cols-2 gap-2 md:grid-cols-5" role="radiogroup" aria-label="Mode akses">
        {#each PRESETS as p (p.id)}
          <button type="button" role="radio" aria-checked={preset === p.id} disabled={!!p.soon} data-testid={`access-mode-${p.id}`}
            class="rounded-xl border p-2.5 text-left disabled:cursor-not-allowed disabled:opacity-60 {preset === p.id ? 'border-green-500 ring-1 ring-inset ring-green-500' : 'border-white-300 dark:border-navy-600'}"
            onclick={() => pick(p.id)}>
            <b class="block text-sm text-black-900 dark:text-white-100">{p.name}{#if p.soon}<span class="ml-1 text-[10px] font-normal text-cau-500">segera</span>{/if}</b>
            <span class="text-xs text-black-800 dark:text-black-600">{p.hint}</span>
          </button>
        {/each}
      </div>
    </section>

    <section class={card}>
      <p class="text-sm font-semibold text-black-900 dark:text-white-100">2. Siapa dapat apa</p>
      <p class={hint}>Baris = connector. Kolom = siapa yang memanggil. Isi = boleh apa, dan jalan sebagai siapa.</p>
      <div class="mt-2 overflow-x-auto">
        <table class="w-full border-collapse text-sm" data-testid="access-roles-table">
          <thead>
            <tr class="text-left text-xs uppercase tracking-wide text-black-800 dark:text-black-600">
              <th class="py-2 pr-2 font-medium">Connector</th>
              <th class="py-2 pr-2 font-medium">Kamu (pemilik)</th>
              <th class="py-2 pr-2 font-medium">Teman tim</th>
              <th class="py-2 pr-2 font-medium">Siapa saja / guest</th>
              <th class="py-2 pr-2 font-medium">Akun dipinjam</th>
            </tr>
          </thead>
          <tbody>
            {#each rows as c (c.id)}
              {@const g = grantOf(c.id)}
              {@const own = cellFor("owner", mode, g)}
              {@const mate = cellFor("mate", mode, g)}
              {@const guest = cellFor("guest", mode, g)}
              {@const st = lentStatus(ownerView.find((r) => r.connector_id === c.id))}
              <tr class="border-t border-white-300 align-top dark:border-navy-600" data-testid={`access-row-${c.id}`}>
                <td class="py-2 pr-2">
                  <b class="block text-black-900 dark:text-white-100">{c.label}</b>
                  <div class="mt-1 flex flex-wrap items-center gap-2">
                    <select class={select} aria-label={`Level ${c.label}`} value={g?.level ?? (mode === "owner" ? "all" : "off")}
                      disabled={g?.level === "pick"}
                      onchange={(e) => editRow(c.id, (e.currentTarget as HTMLSelectElement).value as ConnectorGrant["level"], !!g?.lend)}>
                      <option value="off">mati</option><option value="read">baca</option><option value="all">semua</option>
                      {#if g?.level === "pick"}<option value="pick">pilihan op</option>{/if}
                    </select>
                    <label class="flex items-center gap-1 text-xs text-black-800 dark:text-black-600">
                      <input type="checkbox" checked={!!g?.lend} disabled={own.level === "off"} data-testid={`access-lend-${c.id}`}
                        onchange={(e) => editRow(c.id, (g?.level ?? (mode === "owner" ? "all" : "read")) as ConnectorGrant["level"], (e.currentTarget as HTMLInputElement).checked)} />
                      pinjamkan
                    </label>
                  </div>
                </td>
                <td class="py-2 pr-2"><span class="{pill} {lvCls(own.level)}">{levelName(own.level)}</span><small class="mt-1 block text-xs text-black-800 dark:text-black-600">{own.as}</small></td>
                <td class="py-2 pr-2"><span class="{pill} {lvCls(mate.level)}">{levelName(mate.level)}</span><small class="mt-1 block text-xs text-black-800 dark:text-black-600">{mate.as}</small>
                  <small class="mt-1 block text-[11px] text-black-700 dark:text-black-700">tulis minta persetujuan · <span class="text-cau-500">segera</span></small></td>
                <td class="py-2 pr-2"><span class="{pill} {lvCls(guest.level)}">{levelName(guest.level)}</span><small class="mt-1 block text-xs text-black-800 dark:text-black-600">{guest.as}</small></td>
                <td class="py-2 pr-2 text-xs {st.cls}">{st.text}</td>
              </tr>
            {:else}
              <tr><td colspan="5" class="py-3 {hint}">Belum ada connector milikmu untuk diatur.</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
      <p class="mt-2 border-l-2 border-green-500 pl-2 {hint}">Guest berbeda dari teman tim, tier kustom, dan approval tulis butuh tier per orang di server · <span class="text-cau-500">segera</span>. Sampai itu ada, teman tim dan guest sama-sama memakai pinjaman bila tidak punya akses sendiri.</p>
    </section>

    {#if diff.length > 0}
      <section class="{card} border-green-500" data-testid="access-diff">
        <p class="text-sm font-semibold text-black-900 dark:text-white-100">Perubahan belum disimpan</p>
        <ul class="mt-1 list-disc pl-5 text-xs text-black-900 dark:text-white-100">{#each diff as d (d)}<li>{d}</li>{/each}</ul>
        <div class="mt-3 flex gap-2">
          {#if confirming}
            <button type="button" class="rounded-lg bg-green-600 px-3 py-1.5 text-sm text-white-100" data-testid="access-save-confirm" onclick={save}>Ya, simpan {diff.length} perubahan</button>
            <button type="button" class="rounded-lg px-3 py-1.5 text-sm text-black-800 dark:text-black-600" onclick={() => (confirming = false)}>Kembali</button>
          {:else}
            <button type="button" class="rounded-lg bg-green-600 px-3 py-1.5 text-sm text-white-100" data-testid="access-save" onclick={() => (confirming = true)}>Simpan</button>
            <button type="button" class="rounded-lg px-3 py-1.5 text-sm text-black-800 dark:text-black-600" onclick={reset}>Batal</button>
          {/if}
        </div>
      </section>
    {/if}

    <div class="grid gap-4 md:grid-cols-2">
      <section class={card} data-testid="access-preview">
        <p class="text-sm font-semibold text-black-900 dark:text-white-100">3. Pratinjau sebagai…</p>
        <p class={hint}>Persis yang dilihat agent untuk orang itu, dari pengaturan yang tersimpan.</p>
        <select class="{select} mt-2" bind:value={previewAs} aria-label="Pratinjau sebagai">
          <option value="owner">Kamu (pemilik)</option>
          <option value="guest">Siapa saja / guest</option>
          {#each users as u (u.id)}<option value={`user:${u.id}`}>{u.name || u.id}</option>{/each}
        </select>
        {#if previewError}<p class="mt-2 text-xs text-neg-400">{previewError}</p>{/if}
        <ul class="mt-2 space-y-1 text-xs">
          {#each preview ?? [] as r (r.connector_id)}
            <li class={r.runs_as === "none" ? "text-black-700 line-through dark:text-black-600" : "text-black-900 dark:text-white-100"}>
              <b>{r.label}</b> · {levelName(r.runs_as === "none" ? "off" : r.level)}
              {#if r.runs_as === "self"} · aksesnya sendiri{:else if r.runs_as === "owner"} · akun pemilik{r.account ? ` (${r.account})` : ""}{/if}
              {#if r.ops.length > 0}<span class="text-black-800 dark:text-black-600"> — {r.ops.join(", ")}</span>{/if}
            </li>
          {/each}
        </ul>
      </section>

      <section class={card} data-testid="access-trail">
        <p class="text-sm font-semibold text-black-900 dark:text-white-100">4. Jejak: siapa memakai atas nama siapa</p>
        <p class={hint}>Panggilan terakhir yang jalan lewat pinjaman.</p>
        {#if trail && trail.length > 0}
          <table class="mt-2 w-full text-xs">
            <tbody>
              {#each trail as t (t.run_id)}
                <tr class="border-t border-white-300 dark:border-navy-600">
                  <td class="py-1.5 pr-2 text-black-800 dark:text-black-600">{new Date(t.at).toLocaleString()}</td>
                  <td class="py-1.5 pr-2 text-black-900 dark:text-white-100">{t.caller_name || t.caller_id}</td>
                  <td class="py-1.5 pr-2 text-black-900 dark:text-white-100">{t.connector_label || t.connector_id} · {t.operation}</td>
                  <td class="py-1.5 text-black-800 dark:text-black-600">atas nama {t.on_behalf_of_name || t.on_behalf_of_id}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        {:else}
          <p class="mt-2 {hint}">{trail === null ? "Memuat…" : "Belum ada panggilan lewat pinjaman."}</p>
        {/if}
      </section>
    </div>
  {/if}
</div>
