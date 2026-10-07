<script lang="ts">
  /* Admin → Plugins (/admin/plugins). Installed (default): a compact table of
     every installed plugin across kinds — search (`/` focuses it), Kind /
     Origin / Status facets with counts, sort, pagination, a per-row actions
     menu and bulk update / enable / disable — with search, facets, sort and
     page kept in the query string. Sources: the read-only official catalog
     plus url / GitHub sources with Test (six-step health check), Check now,
     Edit, Delete. Marketplace: a card grid of everything the official
     catalog and the enabled sources offer — installed plugins stay listed
     with Installed / Update / installed-from-elsewhere state — with source,
     install-state and sort filters. Uninstall works for every kind, from
     Installed and Marketplace. Add: Upload a zip, Link (plugins.json or a .zip,
     installed once) or GitHub release (public, or private with a PAT that is
     write-only). Everyone logged in can read; actions are admin-only
     server-side and hidden here for non-admins. */
  import { Button, TextInput, KebabMenu, ConfirmDialog } from "@wick-fe/common-ui";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import {
    listPluginSources, savePluginSource, deletePluginSource, testPluginSource, testPluginSourceInput, checkPluginSource,
    listAvailablePlugins, installFromSource, uploadPluginZip,
    listInstalledPlugins, listPlugins, installPlugin, updatePluginStream, setPluginEnabled, removePlugin, serviceAction, listServicePlugins,
    type PluginSource, type PluginSourceInput, type SourceStep, type AvailablePlugin,
    type InstalledPlugin, type OfficialCatalog, type PluginOrigin, type PluginProgress,
  } from "$lib/api.js";
  import PluginUpdateProgress from "./PluginUpdateProgress.svelte";
  import { pluginPhaseLabel } from "./updateProgress.js";
  import {
    parseQuery, queryString, filterInstalled, facetCounts, sortInstalled, paginate, matchesText,
    canUpdate, hasError, hasUpdateSource, KINDS, ORIGINS, STATUSES, SORTS, PAGE_SIZES,
    marketRows, filterMarket, sortMarket, matchesMarket, uninstallBody, MARKET_FILTERS, MARKET_SORTS,
    type Tab, type ListQuery, type Page, type MarketRow,
  } from "./pluginsList.js";
  import type { PluginEntry } from "$lib/types.js";
  import { push } from "$lib/router.js";

  type Method = "upload" | "link" | "github";
  let query = $state<ListQuery>(parseQuery(window.location.search));
  let checkSummary = $state("");
  let qInput = $state(query.q);
  let installed = $state<InstalledPlugin[]>([]);
  let official = $state<OfficialCatalog | null>(null);
  let officialCatalog = $state<PluginEntry[]>([]);
  let progress = $state<Record<string, PluginProgress>>({});
  let sources = $state<PluginSource[]>([]);
  let available = $state<AvailablePlugin[]>([]);
  let isAdmin = $state(false);
  let loading = $state(true);
  let busy = $state("");
  let tests = $state<Record<string, SourceStep[]>>({});
  let selected = $state<string[]>([]);
  let removing = $state<InstalledPlugin | null>(null);
  let searchEl = $state<HTMLInputElement | null>(null);

  let method = $state<Method>("github");
  let editing = $state<string | null>(null);
  let form = $state<PluginSourceInput & { visibility: "public" | "private" }>(blankForm());
  let file = $state<File | null>(null);

  function blankForm() {
    return { type: "github" as const, url: "", repo: "", visibility: "public" as const, pat: "", pub_key: "", key_filter: "", allow_prerelease: false, auto_update: false, poll_minutes: 30 };
  }

  // Query string mirrors the list state; replaceState so filtering does not
  // flood the back button.
  $effect(() => {
    const qs = queryString(query);
    if (qs !== window.location.search && !(qs === "" && window.location.search === "?")) {
      history.replaceState(history.state, "", window.location.pathname + qs + window.location.hash);
    }
  });
  // Debounced search: typing updates qInput at once, the list follows 200 ms later.
  $effect(() => {
    const v = qInput;
    const t = setTimeout(() => {
      if (v !== query.q) setQuery({ q: v });
    }, 200);
    return () => clearTimeout(t);
  });

  // Any change that reshapes the list sends it back to page 1.
  function setQuery(patch: Partial<ListQuery>): void {
    query = { ...query, page: 1, ...patch };
  }
  function setTab(t: Tab): void {
    query = { ...query, tab: t, page: 1 };
  }
  function resetFilters(): void {
    qInput = "";
    setQuery({ q: "", kind: "all", origin: "all", status: "all", avail: "all", src: "all" });
  }

  function onKey(e: KeyboardEvent): void {
    if (e.key !== "/" || e.ctrlKey || e.metaKey || e.altKey) return;
    const t = e.target;
    if (t instanceof Element && t.closest("input, textarea, select, [contenteditable='true']")) return;
    if (!searchEl) return;
    e.preventDefault();
    searchEl.focus();
  }

  /* Supervisor state per service key, for the Sleeping badge. */
  let serviceStates = $state<Record<string, string>>({});

  async function load(): Promise<void> {
    try {
      // The catalog list is best-effort: its fetch failing must not hide
      // the installed plugins or the sources.
      const [s, a, i, c, svc] = await Promise.all([
        listPluginSources(), listAvailablePlugins(), listInstalledPlugins(), listPlugins().catch(() => null),
        // Live service states (Sleeping badge); best-effort as well.
        Promise.resolve().then(() => listServicePlugins()).catch(() => null),
      ]);
      serviceStates = Object.fromEntries((svc ?? []).map((x) => [x.key, x.status.state]));
      sources = s.sources;
      available = a.available;
      installed = i.plugins;
      official = i.official;
      officialCatalog = c?.catalog ?? c?.available ?? [];
      isAdmin = s.is_admin;
      const ids = new Set(i.plugins.map(rowID));
      selected = selected.filter((id) => ids.has(id));
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }
  $effect(() => {
    load();
  });

  async function run(label: string, fn: () => Promise<void>): Promise<void> {
    busy = label;
    try {
      await fn();
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  const test = (s: PluginSource) => run(`test:${s.id}`, async () => {
    tests = { ...tests, [s.id]: (await testPluginSource(s.id)).steps };
  });
  const check = (s: PluginSource) => run(`check:${s.id}`, async () => {
    const res = await checkPluginSource(s.id);
    toastOk(res.updates?.length ? `Updates: ${res.updates.join(", ")}` : `${s.name} checked`);
    await load();
  });
  const remove = (s: PluginSource) => run(`del:${s.id}`, async () => {
    if (!confirm(`Remove source ${s.name}? Installed plugins stay, without updates.`)) return;
    await deletePluginSource(s.id);
    await load();
  });
  const install = (a: AvailablePlugin) => run(`install:${a.key}`, async () => {
    const res = await installFromSource(a.source_id, a.key);
    toastOk(`Installed ${a.key} v${res.version}`);
    await load();
  });

  const installOfficial = (e: PluginEntry) => run(`install:${e.key}`, async () => {
    await installPlugin(e.key);
    toastOk(`Installed ${e.key} v${e.version}`);
    await load();
  });
  // A connector and a tool may share a key, so rows are told apart by kind too.
  const rowID = (p: InstalledPlugin) => `${p.kind}:${p.key}`;
  async function updateOne(p: InstalledPlugin): Promise<void> {
    // Same stream + progress phases as the connector detail kebab.
    progress = { ...progress, [rowID(p)]: { phase: "downloading", pct: 0 } };
    try {
      await updatePluginStream(p.key, (pr) => {
        progress = { ...progress, [rowID(p)]: pr };
      });
    } finally {
      const { [rowID(p)]: _, ...rest } = progress;
      progress = rest;
    }
  }
  const update = (p: InstalledPlugin) => run(`update:${rowID(p)}`, async () => {
    await updateOne(p);
    toastOk(`Updated ${p.key} to v${p.latest_version}`);
    await load();
  });
  // Check updates: one source, or every enabled source. The official catalog
  // has no check endpoint; reloading re-reads it (cached a few minutes).
  const checkUpdates = (sourceID?: string) => run(sourceID ? `check:${sourceID}` : "check:all", async () => {
    const ids = sourceID ? [sourceID] : sources.filter((s) => s.enabled).map((s) => s.id);
    const res = await Promise.allSettled(ids.map((id) => checkPluginSource(id)));
    const failed = res.filter((r) => r.status === "rejected").length;
    await load();
    const n = installed.filter((p) => p.update_available).length;
    checkSummary = failed
      ? `${failed} source(s) failed to check${n ? ` · ${n} update(s) available` : ""}`
      : n ? `${n} update(s) available` : "Everything is up to date";
    if (failed) toastError(checkSummary);
    else toastOk(checkSummary);
  });
  // Enable / disable / remove go through the plugin store, which manages
  // connector plugins; tool, job and service plugins are managed from their
  // own pages, so the menu only offers these on connectors.
  const toggle = (p: InstalledPlugin) => run(`toggle:${rowID(p)}`, async () => {
    await setPluginEnabled(p.key, !p.enabled);
    toastOk(`${p.enabled ? "Disabled" : "Enabled"} ${p.key}`);
    await load();
  });
  const restart = (p: InstalledPlugin) => run(`restart:${rowID(p)}`, async () => {
    await serviceAction(p.key, "restart");
    toastOk(`Restarted ${p.key}`);
    await load();
  });
  const confirmRemove = () => {
    const p = removing;
    removing = null;
    if (!p) return;
    run(`remove:${rowID(p)}`, async () => {
      await removePlugin(p.key, p.kind);
      toastOk(`Uninstalled ${p.key}`);
      await load();
    });
  };

  // Bulk: each action only touches the selected rows it applies to and says
  // how many it skipped, rather than failing the whole batch.
  const selectedRows = $derived(installed.filter((p) => selected.includes(rowID(p))));
  function bulk(label: string, applies: (p: InstalledPlugin) => boolean, act: (p: InstalledPlugin) => Promise<unknown>, verb: string) {
    return run(`bulk:${label}`, async () => {
      const targets = selectedRows.filter(applies);
      let failed = 0;
      for (const p of targets) {
        try {
          await act(p);
        } catch {
          failed++;
        }
      }
      const skipped = selectedRows.length - targets.length;
      await load();
      const msg = `${verb} ${targets.length - failed}` + (skipped ? ` · skipped ${skipped}` : "") + (failed ? ` · failed ${failed}` : "");
      if (failed) toastError(msg);
      else toastOk(msg);
    });
  }
  const bulkUpdate = () => bulk("update", canUpdate, updateOne, "Updated");
  const bulkEnable = (on: boolean) =>
    bulk(on ? "enable" : "disable", (p) => p.kind === "connector" && p.enabled !== on, (p) => setPluginEnabled(p.key, on), on ? "Enabled" : "Disabled");

  function toggleSelect(p: InstalledPlugin): void {
    const id = rowID(p);
    selected = selected.includes(id) ? selected.filter((x) => x !== id) : [...selected, id];
  }

  const facets = $derived(facetCounts(installed, query));
  const shown = $derived(sortInstalled(filterInstalled(installed, query), query.sort));
  const installedPage = $derived(paginate(shown, query.page, query.size));
  const pageIDs = $derived(installedPage.rows.map(rowID));
  const allOnPage = $derived(pageIDs.length > 0 && pageIDs.every((id) => selected.includes(id)));
  function togglePage(): void {
    selected = allOnPage ? selected.filter((id) => !pageIDs.includes(id)) : [...new Set([...selected, ...pageIDs])];
  }
  const filtered = $derived(query.q.trim() !== "" || query.kind !== "all" || query.origin !== "all" || query.status !== "all");

  const shownSources = $derived(sources.filter((s) => matchesText(query.q, s.name, s.url, s.repo)));
  const officialShown = $derived(!!official && matchesText(query.q, "Official wick", official.url));
  const sourcesPage = $derived(paginate(shownSources, query.page, query.size));
  const checkedAt = $derived<Record<string, string | undefined>>({
    official: official?.last_check_at, ...Object.fromEntries(sources.map((s) => [s.id, s.last_check_at])),
  });
  const market = $derived(marketRows(available, officialCatalog, installed, checkedAt));
  const marketShown = $derived(sortMarket(filterMarket(market, query), query.msort));
  const marketPage = $derived(paginate(marketShown, query.page, query.size));
  const marketCounts = $derived(Object.fromEntries(MARKET_FILTERS.map((f) => [f, filterMarket(market, { ...query, avail: f }).length])) as Record<string, number>);
  const notInstalled = $derived(market.filter((r) => matchesMarket(r, "not-installed")).length);

  const counts = $derived({
    total: installed.length,
    active: installed.filter((p) => p.enabled).length,
    disabled: installed.filter((p) => !p.enabled).length,
    updates: installed.filter((p) => p.update_available).length,
    errors: installed.filter(hasError).length,
    official: installed.filter((p) => p.origin === "official").length,
  });

  // Detail pages live in the manager SPA. Hosted under /admin, the SPA router
  // cannot render them in place, so leave for the real URL instead.
  /* Why the Marketplace is empty: no source, a failing source, a source
     never checked, or the sources offer nothing. Installed plugins stay
     listed, so "everything is installed" is never the reason. */
  let availableEmptyReason = $derived.by(() => {
    if (!sources.length && !official) return "No sources yet — add one under Add new plugin, then Check now.";
    const bad = sources.filter((s) => s.last_status === "error");
    if (bad.length) return `${bad.map((s) => s.name).join(", ")} failed its last check (${bad[0].last_error || "unknown error"}) — fix it under Sources, then Check now.`;
    if (official?.error) return `The official catalog failed to load (${official.error}).`;
    if (sources.some((s) => !s.last_check_at)) return "Some sources were never checked — run Check now on the Sources tab.";
    return "The sources offer no plugins yet.";
  });

  function openDetail(p: InstalledPlugin): void {
    const el = document.getElementById("app");
    if (el?.dataset.embed) window.location.assign((el.dataset.base ?? "/manager") + p.detail_path);
    else push(p.detail_path);
  }
  function onRowClick(e: MouseEvent, p: InstalledPlugin): void {
    if ((e.target as HTMLElement).closest("button, a, input, label")) return;
    openDetail(p);
  }

  function rowMenu(p: InstalledPlugin) {
    const items: { label: string; onclick: () => void; danger?: boolean; divider?: boolean; disabled?: boolean }[] = [
      { label: "Open details", onclick: () => openDetail(p) },
    ];
    if (isAdmin) {
      if (canUpdate(p)) items.push({ label: `Update to v${p.latest_version}`, onclick: () => update(p), disabled: busy !== "" });
      if (p.source_id && (p.origin === "source" || p.origin === "url-zip")) {
        items.push({ label: "Check for update", onclick: () => checkUpdates(p.source_id), disabled: busy !== "" });
      }
      if (p.kind === "connector") items.push({ label: p.enabled ? "Disable" : "Enable", onclick: () => toggle(p), disabled: busy !== "" });
      if (p.kind === "service") items.push({ label: "Restart service", onclick: () => restart(p), disabled: busy !== "" || !p.enabled });
    }
    if (p.source_url) items.push({ label: p.origin === "official" ? "Catalog ↗" : "Source ↗", onclick: () => window.open(p.source_url, "_blank", "noopener") });
    if (p.download_url) items.push({ label: "Download ↗", onclick: () => window.open(p.download_url, "_blank", "noopener") });
    if (isAdmin) items.push({ label: "Uninstall…", onclick: () => (removing = p), danger: true, divider: true, disabled: busy !== "" });
    return items;
  }

  // Marketplace card menu: only for a plugin that is installed.
  function marketMenu(p: InstalledPlugin) {
    const items: { label: string; onclick: () => void; danger?: boolean; divider?: boolean; disabled?: boolean }[] = [
      { label: "Open details", onclick: () => openDetail(p) },
    ];
    if (isAdmin) items.push({ label: "Uninstall…", onclick: () => (removing = p), danger: true, divider: true, disabled: busy !== "" });
    return items;
  }
  const installRow = (r: MarketRow) => (r.src ? install(r.src) : r.off ? installOfficial(r.off) : undefined);
  const marketLabel: Record<string, string> = { all: "All", "not-installed": "Not installed", installed: "Installed", update: "Update available" };
  const marketSortLabel: Record<string, string> = { name: "Name", updated: "Recently updated" };
  const badgeClass = "rounded-full px-2 py-0.5 text-[10px] font-medium";

  function originLabel(p: InstalledPlugin): string {
    switch (p.origin) {
      case "official": return "Official";
      case "source": return p.source_name || "Source removed";
      case "url-zip": return "Zip link";
      case "upload": return "Upload";
      default: return "Local";
    }
  }
  const originClass: Record<PluginOrigin, string> = {
    official: "bg-green-200 text-green-700 dark:bg-green-800 dark:text-green-300",
    source: "bg-prog-100 text-prog-400",
    "url-zip": "bg-prog-100 text-prog-400",
    upload: "bg-amber-50 text-amber-700 dark:bg-amber-900 dark:text-amber-300",
    local: "bg-white-300 text-black-700 dark:bg-navy-600 dark:text-black-600",
  };
  const kindIcon: Record<InstalledPlugin["kind"], string> = { connector: "🔌", tool: "🛠", job: "⏱", service: "⚙" };
  const filterLabel = (v: string) => (v === "all" ? "All" : v === "update" ? "Update available" : v === "error" ? "Health failing" : v[0].toUpperCase() + v.slice(1));
  const sortLabel: Record<string, string> = { name: "Name", kind: "Kind", checked: "Last checked", update: "Updates first" };
  const chipClass = (on: boolean) =>
    `rounded-full border px-2.5 py-1 text-xs font-medium ${on ? "border-green-600 bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-300" : "border-white-300 bg-white-100 text-black-800 hover:border-green-400 dark:border-navy-600 dark:bg-navy-700 dark:text-black-600"}`;
  const selectClass = "rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1.5 text-xs text-black-900 dark:text-white-100";
  function edit(s: PluginSource): void {
    editing = s.id;
    method = s.type === "github" ? "github" : "link";
    form = { ...blankForm(), type: s.type, url: s.url ?? "", repo: s.repo ?? "", visibility: s.private ? "private" : "public",
      pub_key: s.pub_key ?? "", key_filter: s.key_filter ?? "", allow_prerelease: s.allow_prerelease, auto_update: s.auto_update, poll_minutes: s.poll_minutes };
    setTab("add");
  }

  // Add form check result: steps from Test or from a rejected Add, shown
  // inline under the form. The form keeps its values on failure.
  let formSteps = $state<SourceStep[] | null>(null);
  let formError = $state("");

  function sourceInput(): PluginSourceInput {
    return {
      type: method === "github" ? "github" : "url", url: form.url, repo: form.repo, private: form.visibility === "private",
      pat: form.pat || undefined, pub_key: form.pub_key, key_filter: form.key_filter,
      allow_prerelease: form.allow_prerelease, auto_update: form.auto_update, poll_minutes: form.poll_minutes,
    };
  }

  // Steps a 422 Add carries in its body ({error, steps}); null otherwise.
  function errorSteps(e: unknown): SourceStep[] | null {
    try {
      const body = JSON.parse((e as { body?: string }).body ?? "");
      return Array.isArray(body.steps) ? body.steps : null;
    } catch {
      return null;
    }
  }

  const testForm = () => run("test-form", async () => {
    formError = "";
    formSteps = null;
    try {
      formSteps = (await testPluginSourceInput(sourceInput(), editing ?? undefined)).steps;
    } catch (e) {
      formError = e instanceof Error ? e.message : String(e);
    }
  });

  const submit = () => run("save", async () => {
    formError = "";
    if (method === "upload") {
      if (!file) throw new Error("Choose a .zip first");
      const res = await uploadPluginZip(file);
      toastOk(`Installed ${res.key} (${res.kind}) v${res.version}`);
      file = null;
    } else {
      const input = sourceInput();
      let saved: PluginSource;
      try {
        saved = await savePluginSource(input, editing ?? undefined);
      } catch (e) {
        // Keep every field (PAT included) so the admin fixes and resends.
        formError = e instanceof Error ? e.message : String(e);
        formSteps = errorSteps(e) ?? formSteps;
        return;
      }
      // A bare .zip link is a one-off install: fetch it and install now.
      if (input.type === "url" && /\.zip(\?|$)/i.test(form.url ?? "")) {
        await checkPluginSource(saved.id);
        const a = (await listAvailablePlugins()).available.find((x) => x.source_id === saved.id);
        if (a) await installFromSource(saved.id, a.key);
      } else {
        await checkPluginSource(saved.id).catch(() => {});
      }
      toastOk(editing ? "Source saved" : "Source added");
    }
    editing = null;
    form = blankForm();
    formSteps = null;
    setTab(method === "upload" ? "installed" : "sources");
    await load();
  });

  const icon = { ok: "✅", fail: "❌", skip: "⏭" } as const;
  const fmt = (t?: string) => (t ? new Date(t).toLocaleString() : "never");
  const tabClass = (on: boolean) =>
    `px-3 py-2 text-sm font-medium border-b-2 ${on ? "border-green-600 text-black-900 dark:text-white-100" : "border-transparent text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100"}`;
  const methodClass = (on: boolean) =>
    `flex-1 rounded-lg border px-3 py-2 text-left text-sm ${on ? "border-green-600 bg-green-50 dark:bg-green-900/20" : "border-white-300 dark:border-navy-600"}`;
</script>

<svelte:window onkeydown={onKey} />

{#snippet stepList(steps: SourceStep[], testid: string)}
  <ol class="mt-3 divide-y divide-white-300 dark:divide-navy-600 rounded-lg border border-white-300 dark:border-navy-600" data-testid={testid}>
    {#each steps as st (st.n)}
      <li class="flex items-start gap-3 px-3 py-2 text-sm">
        <span>{icon[st.status]}</span>
        <span class="w-32 flex-shrink-0 font-medium text-black-900 dark:text-white-100">{st.n}. {st.name}</span>
        <span class="min-w-0 break-words {st.status === 'fail' ? 'text-red-600 dark:text-red-400' : 'text-black-800 dark:text-black-600'}">{st.message}</span>
      </li>
    {/each}
  </ol>
{/snippet}

{#snippet searchBox(placeholder: string)}
  <div class="relative min-w-0 flex-1 sm:max-w-sm">
    <input
      bind:this={searchEl}
      bind:value={qInput}
      type="search"
      aria-label="Search plugins"
      {placeholder}
      class="w-full rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 py-1.5 pl-3 pr-8 text-sm text-black-900 dark:text-white-100 placeholder:text-black-600 focus:border-green-500 focus:outline-none"
      data-testid="plugin-search"
    />
    <kbd class="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 rounded border border-white-300 dark:border-navy-600 px-1 font-mono text-[10px] text-black-600">/</kbd>
  </div>
{/snippet}

{#snippet pager(pg: Page<unknown>)}
  {#if pg.total > 0}
    <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-black-700 dark:text-black-600" data-testid="pager">
      <span data-testid="pager-info">{pg.from}–{pg.to} of {pg.total}</span>
      <div class="flex items-center gap-2">
        <label class="flex items-center gap-1">Per page
          <select class={selectClass} aria-label="Per page" value={query.size} onchange={(e) => setQuery({ size: Number((e.currentTarget as HTMLSelectElement).value) })}>
            {#each PAGE_SIZES as n (n)}<option value={n}>{n}</option>{/each}
          </select>
        </label>
        <Button variant="secondary" size="sm" onclick={() => (query = { ...query, page: pg.page - 1 })} disabled={pg.page <= 1}>‹ Prev</Button>
        <span>{pg.page} / {pg.pages}</span>
        <Button variant="secondary" size="sm" onclick={() => (query = { ...query, page: pg.page + 1 })} disabled={pg.page >= pg.pages}>Next ›</Button>
      </div>
    </div>
  {/if}
{/snippet}

{#snippet noMatch(what: string)}
  <div class="px-4 py-8 text-center text-sm text-black-700 dark:text-black-600" data-testid="empty-state">
    <p>{what}</p>
    <button class="mt-2 text-xs font-medium text-green-700 hover:underline dark:text-green-300" onclick={resetFilters}>Reset search and filters</button>
  </div>
{/snippet}

<div class="space-y-4" data-testid="plugins-admin">
  <div>
    <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Plugins</h1>
    <p class="mt-0.5 text-sm text-black-800 dark:text-black-600">Connector, tool, job and service plugins installed on this wick, where each came from, and the sources it installs and updates them from.</p>
  </div>

  <div class="flex gap-1 overflow-x-auto border-b border-white-300 dark:border-navy-600">
    <button class={tabClass(query.tab === "installed")} onclick={() => setTab("installed")}>Installed <span class="text-xs text-black-700">{installed.length}</span></button>
    <button class={tabClass(query.tab === "marketplace")} onclick={() => setTab("marketplace")}>Marketplace <span class="text-xs text-black-700">{market.length}</span></button>
    <button class={tabClass(query.tab === "sources")} onclick={() => setTab("sources")}>Sources <span class="text-xs text-black-700">{sources.length + (official ? 1 : 0)}</span></button>
    {#if isAdmin}
      <button class={tabClass(query.tab === "add")} onclick={() => { setTab("add"); editing = null; form = blankForm(); }}>Add new plugin</button>
    {/if}
  </div>

  {#if loading}
    <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if query.tab === "installed"}
    <div class="flex flex-wrap items-center gap-1.5" data-testid="installed-summary">
      <button class={chipClass(!filtered)} onclick={resetFilters}>{counts.total} installed</button>
      <button class={chipClass(query.status === "active")} onclick={() => setQuery({ status: query.status === "active" ? "all" : "active" })}>{counts.active} active</button>
      {#if counts.disabled}<button class={chipClass(query.status === "disabled")} onclick={() => setQuery({ status: query.status === "disabled" ? "all" : "disabled" })}>{counts.disabled} disabled</button>{/if}
      <button class={chipClass(query.status === "update")} onclick={() => setQuery({ status: query.status === "update" ? "all" : "update" })}>{counts.updates} update{counts.updates === 1 ? "" : "s"}</button>
      {#if counts.errors}<button class={chipClass(query.status === "error")} onclick={() => setQuery({ status: query.status === "error" ? "all" : "error" })}><span class="text-red-600 dark:text-red-400">{counts.errors} failing</span></button>{/if}
      <button class={chipClass(query.origin === "official")} onclick={() => setQuery({ origin: query.origin === "official" ? "all" : "official" })}>{counts.official} official</button>
      <span class="px-1 text-xs text-black-700 dark:text-black-600">{counts.total - counts.official} self-installed</span>
    </div>

    {#if official || sources.length}
      <div class="flex flex-wrap items-center gap-1.5 text-[11px] text-black-700 dark:text-black-600" data-testid="source-status">
        <span>Update sources:</span>
        {#if official}
          <span class="rounded-full px-2 py-0.5 {official.error ? 'bg-red-50 text-red-600 dark:bg-red-900 dark:text-red-400' : 'bg-white-300 dark:bg-navy-600'}" title={official.error || `last check ${fmt(official.last_check_at)}`}>Official · {official.error ? "error" : "ok"}</span>
        {/if}
        {#each sources as s (s.id)}
          <span class="rounded-full px-2 py-0.5 {s.last_status === 'error' ? 'bg-red-50 text-red-600 dark:bg-red-900 dark:text-red-400' : 'bg-white-300 dark:bg-navy-600'}" title={s.last_status === "error" ? s.last_error : `last check ${fmt(s.last_check_at)}`}>{s.name} · {s.last_status === "error" ? "error" : s.last_check_at ? `ok, ${fmt(s.last_check_at)}` : "never checked"}</span>
        {/each}
      </div>
    {/if}

    <div class="flex flex-wrap items-center gap-2">
      {@render searchBox("Search name, key, source…")}
      <select class={selectClass} aria-label="Kind" value={query.kind} onchange={(e) => setQuery({ kind: (e.currentTarget as HTMLSelectElement).value as ListQuery["kind"] })}>
        {#each KINDS as k (k)}<option value={k}>{k === "all" ? "All kinds" : filterLabel(k)} ({facets.kind[k]})</option>{/each}
      </select>
      <select class={selectClass} aria-label="Origin" value={query.origin} onchange={(e) => setQuery({ origin: (e.currentTarget as HTMLSelectElement).value as ListQuery["origin"] })}>
        {#each ORIGINS as o (o)}<option value={o}>{o === "all" ? "All origins" : filterLabel(o)} ({facets.origin[o]})</option>{/each}
      </select>
      <select class={selectClass} aria-label="Status" value={query.status} onchange={(e) => setQuery({ status: (e.currentTarget as HTMLSelectElement).value as ListQuery["status"] })}>
        {#each STATUSES as s (s)}<option value={s}>{s === "all" ? "Any status" : filterLabel(s)} ({facets.status[s]})</option>{/each}
      </select>
      <select class={selectClass} aria-label="Sort" value={query.sort} onchange={(e) => (query = { ...query, sort: (e.currentTarget as HTMLSelectElement).value as ListQuery["sort"] })}>
        {#each SORTS as s (s)}<option value={s}>Sort: {sortLabel[s]}</option>{/each}
      </select>
      {#if isAdmin}
        <div class="ml-auto">
          {#if checkSummary}<span class="mr-2 text-xs text-black-700 dark:text-black-600" data-testid="check-summary">{checkSummary}</span>{/if}
          <Button variant="secondary" size="sm" onclick={() => checkUpdates()} disabled={busy !== ""}>{busy === "check:all" ? "Checking…" : "Check updates"}</Button>
        </div>
      {/if}
    </div>

    {#if isAdmin && selected.length}
      <div class="flex flex-wrap items-center gap-2 rounded-lg border border-green-400 bg-green-50 dark:bg-green-900/20 px-3 py-2 text-sm" data-testid="bulk-bar">
        <span class="font-medium text-black-900 dark:text-white-100">{selected.length} selected</span>
        <Button size="sm" onclick={bulkUpdate} disabled={busy !== ""}>{busy === "bulk:update" ? "Updating…" : "Update"}</Button>
        <Button variant="secondary" size="sm" onclick={() => bulkEnable(true)} disabled={busy !== ""}>Enable</Button>
        <Button variant="secondary" size="sm" onclick={() => bulkEnable(false)} disabled={busy !== ""}>Disable</Button>
        <button class="ml-auto text-xs text-black-700 hover:underline dark:text-black-600" onclick={() => (selected = [])}>Clear</button>
      </div>
    {/if}

    <div class="overflow-hidden rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
      {#if installedPage.rows.length}
        <table class="w-full table-fixed text-sm">
          <thead class="border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 text-left text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">
            <tr>
              {#if isAdmin}<th class="w-8 px-3 py-2"><input type="checkbox" aria-label="Select page" checked={allOnPage} onchange={togglePage} /></th>{/if}
              <th class="px-3 py-2 font-medium">Plugin</th>
              <th class="hidden w-28 px-3 py-2 font-medium md:table-cell">Type</th>
              <th class="hidden w-44 px-3 py-2 font-medium sm:table-cell">Version</th>
              <th class="w-36 px-3 py-2 font-medium">Status</th>
              <th class="hidden w-28 px-3 py-2 font-medium md:table-cell">Origin</th>
              <th class="w-10 px-2 py-2"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-white-300 dark:divide-navy-600">
            {#each installedPage.rows as p (rowID(p))}
              <tr class="cursor-pointer hover:bg-white-200 dark:hover:bg-navy-600/40" data-testid="installed-row" onclick={(e) => onRowClick(e, p)}>
                {#if isAdmin}
                  <td class="px-3 py-2"><input type="checkbox" aria-label={`Select ${p.key}`} checked={selected.includes(rowID(p))} onchange={() => toggleSelect(p)} /></td>
                {/if}
                <td class="max-w-0 px-3 py-2">
                  <div class="flex min-w-0 items-center gap-2">
                    <span class="flex-shrink-0" title={p.kind} aria-label={p.kind}>{kindIcon[p.kind]}</span>
                    <button class="truncate font-medium text-black-900 hover:underline dark:text-white-100" onclick={() => openDetail(p)}>{p.name || p.key}</button>
                    <span class="hidden truncate font-mono text-xs text-black-700 dark:text-black-600 sm:inline">{p.key}</span>
                  </div>
                  {#if progress[rowID(p)]}
                    <PluginUpdateProgress class="mt-1 max-w-xs" progress={progress[rowID(p)]} />
                  {/if}
                </td>
                <td class="hidden whitespace-nowrap px-3 py-2 text-xs capitalize text-black-800 dark:text-black-600 md:table-cell" data-testid="kind-cell">{p.kind}</td>
                <td class="hidden whitespace-nowrap px-3 py-2 font-mono text-xs sm:table-cell">
                  <span class="text-black-800 dark:text-black-600">v{p.version}</span>
                  {#if p.update_available}
                    <span class="rounded-full bg-amber-100 dark:bg-amber-900 px-1.5 py-0.5 font-medium text-amber-700 dark:text-amber-300" data-testid="update-badge">Update v{p.latest_version}</span>
                  {:else if !hasUpdateSource(p)}
                    <span class="text-black-600" title="No update source">·</span>
                  {/if}
                </td>
                <td class="whitespace-nowrap px-3 py-2">
                  {#if hasError(p)}
                    <span class="rounded-full bg-red-50 dark:bg-red-900 px-2 py-0.5 text-[10px] font-medium text-red-600 dark:text-red-400" title={p.last_health_detail || "health check failing"}>Failing</span>
                  {:else if p.enabled}
                    <span class="rounded-full bg-green-200 dark:bg-green-800 px-2 py-0.5 text-[10px] font-medium text-green-700 dark:text-green-300">Active</span>
                    {#if p.kind === "service" && serviceStates[p.key] === "sleeping"}
                      <span class="rounded-full bg-prog-100 px-2 py-0.5 text-[10px] font-medium text-prog-400" data-testid="sleeping-badge" title="Auto-off: asleep, wakes on the next request">Sleeping</span>
                    {/if}
                  {:else}
                    <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">Disabled</span>
                  {/if}
                  {#if busy === `update:${rowID(p)}`}<span class="ml-1 text-[10px] text-black-700">{pluginPhaseLabel(progress[rowID(p)])}</span>{/if}
                </td>
                <td class="hidden px-3 py-2 md:table-cell">
                  <span class="whitespace-nowrap rounded-full px-2 py-0.5 text-[10px] font-medium {originClass[p.origin]}" data-testid="origin-badge" title={hasUpdateSource(p) ? `last check ${fmt(p.last_check_at)}` : "No update source"}>{originLabel(p)}</span>
                </td>
                <td class="px-2 py-1 text-right">
                  <KebabMenu size="sm" width={200} ariaLabel={`Actions for ${p.key}`} items={rowMenu(p)} />
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else if installed.length}
        {@render noMatch("No plugin matches these filters.")}
      {:else}
        <p class="px-4 py-8 text-center text-sm text-black-700 dark:text-black-600">No plugins installed yet.</p>
      {/if}
    </div>
    {@render pager(installedPage)}
  {:else if query.tab === "sources"}
    <div class="flex flex-wrap items-center gap-2">{@render searchBox("Search sources…")}</div>
    <div class="space-y-3">
      {#if official && officialShown}
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4" data-testid="official-source-row">
          <div class="flex flex-wrap items-center gap-2">
            <span class="font-medium text-black-900 dark:text-white-100">Official wick</span>
            <span class="rounded-full bg-green-200 dark:bg-green-800 px-2 py-0.5 text-[10px] font-medium text-green-700 dark:text-green-300">built-in</span>
            <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">read-only</span>
          </div>
          <p class="mt-1 font-mono text-xs text-black-700 dark:text-black-600 truncate">{official.url}</p>
          <p class="mt-1 text-xs text-black-700 dark:text-black-600">
            {official.plugins} plugin(s) · last check {fmt(official.last_check_at)}
            {#if official.error}<span class="text-red-600"> · {official.error}</span>{/if}
          </p>
        </div>
      {/if}
      {#if sources.length === 0}
        <p class="rounded-xl border border-dashed border-white-300 dark:border-navy-600 px-4 py-8 text-center text-sm text-black-700 dark:text-black-600">No sources added yet.</p>
      {:else if shownSources.length === 0}
        {@render noMatch("No source matches this search.")}
      {/if}
      {#each sourcesPage.rows as s (s.id)}
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4" data-testid="source-row">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-medium text-black-900 dark:text-white-100">{s.name}</span>
                <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">{s.type === "github" ? "GitHub" : "URL"}</span>
                {#if s.type === "github"}
                  <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">{s.private ? "private" : "public"}{s.has_pat ? " · PAT ••••" : ""}</span>
                {/if}
                {#if s.pub_key}<span class="rounded-full bg-green-200 dark:bg-green-800 px-2 py-0.5 text-[10px] font-medium text-green-700 dark:text-green-300">signed</span>{/if}
                {#if s.auto_update}<span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700">auto-update</span>{/if}
              </div>
              <p class="mt-1 font-mono text-xs text-black-700 dark:text-black-600 truncate">{s.type === "github" ? `github.com/${s.repo}` : s.url}</p>
              <p class="mt-1 text-xs text-black-700 dark:text-black-600">
                {s.plugins} plugin(s) · every {s.poll_minutes} min · last check {fmt(s.last_check_at)}
                {#if s.last_status === "error"}<span class="text-red-600"> · {s.last_error}</span>{/if}
              </p>
            </div>
            {#if isAdmin}
              <div class="flex flex-shrink-0 flex-wrap items-center gap-2">
                <Button variant="secondary" size="sm" onclick={() => test(s)} disabled={busy !== ""}>{busy === `test:${s.id}` ? "Testing…" : "Test"}</Button>
                <Button variant="secondary" size="sm" onclick={() => check(s)} disabled={busy !== ""}>{busy === `check:${s.id}` ? "Checking…" : "Check now"}</Button>
                <Button variant="secondary" size="sm" onclick={() => edit(s)} disabled={busy !== ""}>Edit</Button>
                <Button variant="danger" size="sm" onclick={() => remove(s)} disabled={busy !== ""}>Delete</Button>
              </div>
            {/if}
          </div>
          {#if tests[s.id]}{@render stepList(tests[s.id], "source-test")}{/if}
        </div>
      {/each}
    </div>
    {@render pager(sourcesPage)}
  {:else if query.tab === "marketplace"}
    <div class="flex flex-wrap items-center gap-1.5" data-testid="market-summary">
      <button class={chipClass(query.avail === "all")} onclick={() => setQuery({ avail: "all" })}>{market.length} in catalog</button>
      <button class={chipClass(query.avail === "not-installed")} onclick={() => setQuery({ avail: query.avail === "not-installed" ? "all" : "not-installed" })}>{notInstalled} not installed</button>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      {@render searchBox("Search the marketplace…")}
      <select class={selectClass} aria-label="Source" value={query.src} onchange={(e) => setQuery({ src: (e.currentTarget as HTMLSelectElement).value })}>
        <option value="all">All sources</option>
        {#if official}<option value="official">Official</option>{/if}
        {#each sources.filter((s) => s.enabled) as s (s.id)}<option value={s.id}>{s.name}</option>{/each}
      </select>
      <select class={selectClass} aria-label="Install state" value={query.avail} onchange={(e) => setQuery({ avail: (e.currentTarget as HTMLSelectElement).value as ListQuery["avail"] })}>
        {#each MARKET_FILTERS as f (f)}<option value={f}>{marketLabel[f]} ({marketCounts[f]})</option>{/each}
      </select>
      <select class={selectClass} aria-label="Sort" value={query.msort} onchange={(e) => (query = { ...query, msort: (e.currentTarget as HTMLSelectElement).value as ListQuery["msort"] })}>
        {#each MARKET_SORTS as s (s)}<option value={s}>Sort: {marketSortLabel[s]}</option>{/each}
      </select>
    </div>
    {#if marketPage.rows.length}
      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3" data-testid="market-grid">
        {#each marketPage.rows as r (r.id)}
          {@const inst = r.installed}
          <div class="flex min-w-0 flex-col rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4" data-testid="market-card" data-state={r.state}>
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="flex min-w-0 items-center gap-2">
                  <span class="flex-shrink-0" title={r.kind} aria-label={r.kind}>{kindIcon[r.kind]}</span>
                  <span class="truncate font-medium text-black-900 dark:text-white-100">{r.name || r.key}</span>
                </div>
                <p class="truncate font-mono text-xs text-black-700 dark:text-black-600">{r.key}</p>
              </div>
              {#if inst}
                <KebabMenu size="sm" width={180} ariaLabel={`Actions for ${r.sourceName} ${r.key}`} items={marketMenu(inst)} />
              {/if}
            </div>
            <div class="mt-2 flex flex-wrap items-center gap-1.5">
              <span class="{badgeClass} bg-white-300 text-black-700 dark:bg-navy-600 dark:text-black-600">{r.kind}</span>
              <span class="{badgeClass} bg-white-300 font-mono text-black-700 dark:bg-navy-600 dark:text-black-600">v{r.version}</span>
            </div>
            <p class="mt-2 line-clamp-2 flex-1 text-xs text-black-800 dark:text-black-600">{r.description || "No description."}</p>
            <p class="mt-2 truncate text-[11px] text-black-700 dark:text-black-600">from {r.sourceName}</p>
            {#if inst && progress[`${inst.kind}:${inst.key}`]}
              <PluginUpdateProgress class="mt-2" progress={progress[`${inst.kind}:${inst.key}`]} />
            {/if}
            <div class="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-white-300 dark:border-navy-600 pt-3" data-testid="market-status">
              {#if r.state === "install"}
                <span class="text-xs text-black-700 dark:text-black-600">Not installed</span>
                {#if isAdmin}<Button size="sm" onclick={() => installRow(r)} disabled={busy !== ""}>{busy === `install:${r.key}` ? "Installing…" : "Install"}</Button>{/if}
              {:else if r.state === "no-arch"}
                <span class="text-xs text-black-700 dark:text-black-600">No build for this host</span>
              {:else if inst}
                <span class="{badgeClass} {r.state === 'other' ? 'bg-white-300 text-black-700 dark:bg-navy-600 dark:text-black-600' : 'bg-green-200 text-green-700 dark:bg-green-800 dark:text-green-300'}">Installed v{inst.version}{r.state === "other" ? ` (from ${originLabel(inst)})` : ""}</span>
                {#if r.state === "update" && isAdmin}
                  <Button size="sm" onclick={() => update(inst)} disabled={busy !== ""}>{busy === `update:${inst.kind}:${inst.key}` ? "Updating…" : `Update to v${r.version}`}</Button>
                {:else if r.state === "update"}
                  <span class="{badgeClass} bg-amber-100 text-amber-700 dark:bg-amber-900 dark:text-amber-300">Update v{r.version}</span>
                {/if}
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {:else if market.length}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">{@render noMatch("No plugin in the marketplace matches these filters.")}</div>
    {:else}
      <p class="rounded-xl border border-dashed border-white-300 dark:border-navy-600 px-4 py-8 text-center text-sm text-black-700 dark:text-black-600" data-testid="available-empty">{availableEmptyReason}</p>
    {/if}
    {@render pager(marketPage)}
  {:else}
    <div class="max-w-2xl space-y-4 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5" data-testid="add-plugin">
      {#if !editing}
        <div class="flex gap-2">
          <button class={methodClass(method === "upload")} onclick={() => (method = "upload")}><b>Upload zip</b><br /><span class="text-xs text-black-700">dev / local test, no updates</span></button>
          <button class={methodClass(method === "link")} onclick={() => (method = "link")}><b>Link</b><br /><span class="text-xs text-black-700">plugins.json or a .zip</span></button>
          <button class={methodClass(method === "github")} onclick={() => (method = "github")}><b>GitHub release</b><br /><span class="text-xs text-black-700">public or private repo</span></button>
        </div>
      {:else}
        <p class="text-sm font-medium text-black-900 dark:text-white-100">Edit source</p>
      {/if}

      {#if method === "upload"}
        <label class="block text-sm text-black-800 dark:text-black-600">Plugin zip (binary + plugin.json, max 100 MB)
          <input class="mt-1 block w-full text-sm" type="file" accept=".zip" onchange={(e) => (file = (e.currentTarget as HTMLInputElement).files?.[0] ?? null)} />
        </label>
      {:else if method === "link"}
        <label class="block space-y-1 text-sm text-black-800 dark:text-black-600"><span>URL</span><TextInput ariaLabel="URL" placeholder="https://example.com/plugins.json" value={form.url ?? ""} onChange={(v) => (form.url = v)} /></label>
        <p class="text-xs text-black-700 dark:text-black-600">A plugins.json URL is polled for updates. A direct .zip link installs once, without updates.</p>
      {:else}
        <label class="block space-y-1 text-sm text-black-800 dark:text-black-600"><span>Repository</span><TextInput ariaLabel="Repository" placeholder="owner/repo" value={form.repo ?? ""} onChange={(v) => (form.repo = v)} /></label>
        <div class="flex gap-4 text-sm text-black-800 dark:text-black-600">
          <label class="flex items-center gap-1.5"><input type="radio" value="public" bind:group={form.visibility} /> Public</label>
          <label class="flex items-center gap-1.5"><input type="radio" value="private" bind:group={form.visibility} /> Private</label>
        </div>
        {#if form.visibility === "private"}
          <label class="block space-y-1 text-sm text-black-800 dark:text-black-600"><span>Personal access token</span><TextInput ariaLabel="Personal access token" type="password" placeholder={editing ? "•••••••• stored — leave empty to keep" : "github_pat_…"} value={form.pat ?? ""} onChange={(v) => (form.pat = v)} /></label>
          <p class="text-xs text-black-700 dark:text-black-600">Fine-grained, this repo only, Contents: Read-only. Stored encrypted, never shown again, sent only to api.github.com.</p>
        {/if}
        <label class="block space-y-1 text-sm text-black-800 dark:text-black-600"><span>Plugin keys (optional)</span><TextInput ariaLabel="Plugin keys (optional)" placeholder="all plugins in the repo" value={form.key_filter ?? ""} onChange={(v) => (form.key_filter = v)} /></label>
      {/if}

      {#if method !== "upload"}
        <label class="block space-y-1 text-sm text-black-800 dark:text-black-600"><span>Publisher key (optional)</span><TextInput ariaLabel="Publisher key (optional)" placeholder="base64 ed25519 — pins signatures" value={form.pub_key ?? ""} onChange={(v) => (form.pub_key = v)} /></label>
        <div class="flex flex-wrap gap-4 text-sm text-black-800 dark:text-black-600">
          {#if method === "github"}<label class="flex items-center gap-1.5"><input type="checkbox" bind:checked={form.allow_prerelease} /> Include prereleases</label>{/if}
          <label class="flex items-center gap-1.5"><input type="checkbox" bind:checked={form.auto_update} /> Auto-update</label>
        </div>
      {/if}

      {#if method !== "upload" && (formError || formSteps)}
        {@const passed = !formError && !formSteps?.some((st) => st.status === "fail")}
        <div class="rounded-lg border px-3 py-2 text-sm {passed ? 'border-green-400 bg-green-50 text-green-700 dark:bg-green-900 dark:text-green-300' : 'border-red-400 bg-red-50 text-red-600 dark:bg-red-900 dark:text-red-400'}" data-testid="form-check" role="status">
          {passed ? "Check passed — Add source uses these same values." : formError || "Check failed — fix the field below and test again."}
        </div>
        {#if formSteps}{@render stepList(formSteps, "form-test")}{/if}
      {/if}

      <div class="flex justify-end gap-2">
        {#if editing}<Button variant="secondary" onclick={() => { editing = null; setTab("sources"); }}>Cancel</Button>{/if}
        {#if method !== "upload"}
          <Button variant="secondary" onclick={testForm} disabled={busy !== ""}>{busy === "test-form" ? "Testing…" : "Test"}</Button>
        {/if}
        <Button onclick={submit} disabled={busy !== ""}>{busy === "save" ? "Saving…" : method === "upload" ? "Upload & install" : editing ? "Save" : "Add source"}</Button>
      </div>
    </div>
  {/if}
</div>

<ConfirmDialog
  open={removing !== null}
  title={`Uninstall ${removing?.name || removing?.key || ""}?`}
  body={uninstallBody(removing?.kind ?? "connector")}
  confirmLabel="Uninstall"
  destructive
  onConfirm={confirmRemove}
  onCancel={() => (removing = null)}
/>
