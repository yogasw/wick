<script lang="ts">
  /* Admin → Plugins. Installed (default): every installed plugin across
     kinds with its origin (official catalog / a source / zip link / upload /
     local-unknown), update state, an in-row Update (same endpoint + progress
     as the detail-page kebab) and token-free source / download links.
     Sources: the read-only official catalog plus url / GitHub sources with
     Test (six-step health check), Check now, Edit, Delete. Available: what
     the official catalog and the sources offer, with Install. Add: Upload a
     zip, Link (plugins.json or a .zip, installed once) or GitHub release
     (public, or private with a PAT that is write-only). Everyone logged in
     can read; actions are admin-only server-side and hidden here for
     non-admins. */
  import { Button, TextInput } from "@wick-fe/common-ui";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import {
    listPluginSources, savePluginSource, deletePluginSource, testPluginSource, testPluginSourceInput, checkPluginSource,
    listAvailablePlugins, installFromSource, uploadPluginZip,
    listInstalledPlugins, listPlugins, installPlugin, updatePluginStream,
    type PluginSource, type PluginSourceInput, type SourceStep, type AvailablePlugin,
    type InstalledPlugin, type OfficialCatalog, type PluginOrigin, type PluginProgress,
  } from "$lib/api.js";
  import PluginUpdateProgress from "./PluginUpdateProgress.svelte";
  import { pluginPhaseLabel } from "./updateProgress.js";
  import type { PluginEntry } from "$lib/types.js";
  import { push } from "$lib/router.js";

  type Tab = "installed" | "sources" | "available" | "add";
  type KindFilter = "all" | InstalledPlugin["kind"];
  type OriginFilter = "all" | "official" | "source" | "upload" | "local";
  type Method = "upload" | "link" | "github";
  let tab = $state<Tab>("installed");
  let installed = $state<InstalledPlugin[]>([]);
  let official = $state<OfficialCatalog | null>(null);
  let officialAvailable = $state<PluginEntry[]>([]);
  let kindFilter = $state<KindFilter>("all");
  let originFilter = $state<OriginFilter>("all");
  let progress = $state<Record<string, PluginProgress>>({});
  let sources = $state<PluginSource[]>([]);
  let available = $state<AvailablePlugin[]>([]);
  let isAdmin = $state(false);
  let loading = $state(true);
  let busy = $state("");
  let tests = $state<Record<string, SourceStep[]>>({});

  let method = $state<Method>("github");
  let editing = $state<string | null>(null);
  let form = $state<PluginSourceInput & { visibility: "public" | "private" }>(blankForm());
  let file = $state<File | null>(null);

  function blankForm() {
    return { type: "github" as const, url: "", repo: "", visibility: "public" as const, pat: "", pub_key: "", key_filter: "", allow_prerelease: false, auto_update: false, poll_minutes: 30 };
  }

  async function load(): Promise<void> {
    try {
      // The catalog list is best-effort: its fetch failing must not hide
      // the installed plugins or the sources.
      const [s, a, i, c] = await Promise.all([
        listPluginSources(), listAvailablePlugins(), listInstalledPlugins(), listPlugins().catch(() => null),
      ]);
      sources = s.sources;
      available = a.available;
      installed = i.plugins;
      official = i.official;
      const have = new Set(i.plugins.map((p) => p.key));
      officialAvailable = (c?.available ?? []).filter((e) => !have.has(e.key));
      isAdmin = s.is_admin;
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
  const update = (p: InstalledPlugin) => run(`update:${rowID(p)}`, async () => {
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
    if (failed) toastError(`${failed} source(s) failed to check`);
    else toastOk(installed.some((p) => p.update_available) ? "Updates available" : "Everything is up to date");
  });

  const originGroup = (o: PluginOrigin): OriginFilter => (o === "url-zip" ? "source" : o);
  const shown = $derived(
    installed.filter((p) => (kindFilter === "all" || p.kind === kindFilter) && (originFilter === "all" || originGroup(p.origin) === originFilter)),
  );
  const counts = $derived({
    total: installed.length,
    active: installed.filter((p) => p.enabled).length,
    official: installed.filter((p) => p.origin === "official").length,
  });
  function originLabel(p: InstalledPlugin): string {
    switch (p.origin) {
      case "official": return "Official wick";
      case "source": return `Source: ${p.source_name || "removed"}`;
      case "url-zip": return "Zip link";
      case "upload": return "Upload";
      default: return "Local / unknown";
    }
  }
  const originClass: Record<PluginOrigin, string> = {
    official: "bg-green-200 text-green-700 dark:bg-green-800 dark:text-green-300",
    source: "bg-prog-100 text-prog-400",
    "url-zip": "bg-prog-100 text-prog-400",
    upload: "bg-amber-50 text-amber-700 dark:bg-amber-900 dark:text-amber-300",
    local: "bg-white-300 text-black-700 dark:bg-navy-600 dark:text-black-600",
  };
  const kinds: KindFilter[] = ["all", "connector", "tool", "job", "service"];
  const origins: OriginFilter[] = ["all", "official", "source", "upload", "local"];
  const filterLabel = (v: string) => (v === "all" ? "All" : v[0].toUpperCase() + v.slice(1));
  const pillClass = (on: boolean) =>
    `rounded-full px-2.5 py-1 text-xs font-medium ${on ? "bg-green-600 text-white-100" : "bg-white-300 text-black-800 hover:text-black-900 dark:bg-navy-600 dark:text-black-600 dark:hover:text-white-100"}`;
  const canUpdate = (p: InstalledPlugin) => p.update_available && p.origin !== "upload" && p.origin !== "local";

  function edit(s: PluginSource): void {
    editing = s.id;
    method = s.type === "github" ? "github" : "link";
    form = { ...blankForm(), type: s.type, url: s.url ?? "", repo: s.repo ?? "", visibility: s.private ? "private" : "public",
      pub_key: s.pub_key ?? "", key_filter: s.key_filter ?? "", allow_prerelease: s.allow_prerelease, auto_update: s.auto_update, poll_minutes: s.poll_minutes };
    tab = "add";
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
    tab = method === "upload" ? "available" : "sources";
    await load();
  });

  const icon = { ok: "✅", fail: "❌", skip: "⏭" } as const;
  const fmt = (t?: string) => (t ? new Date(t).toLocaleString() : "never");
  const tabClass = (on: boolean) =>
    `px-3 py-2 text-sm font-medium border-b-2 ${on ? "border-green-600 text-black-900 dark:text-white-100" : "border-transparent text-black-700 dark:text-black-600 hover:text-black-900"}`;
  const methodClass = (on: boolean) =>
    `flex-1 rounded-lg border px-3 py-2 text-left text-sm ${on ? "border-green-600 bg-green-50 dark:bg-green-900/20" : "border-white-300 dark:border-navy-600"}`;
</script>

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

<div class="space-y-5" data-testid="plugins-admin">
  <div>
    <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Plugins</h1>
    <p class="mt-0.5 text-sm text-black-800 dark:text-black-600">Connector, tool, job and service plugins installed on this wick, where each came from, and the sources it installs and updates them from.</p>
  </div>

  <div class="flex gap-1 overflow-x-auto border-b border-white-300 dark:border-navy-600">
    <button class={tabClass(tab === "installed")} onclick={() => (tab = "installed")}>Installed <span class="text-xs text-black-700">{installed.length}</span></button>
    <button class={tabClass(tab === "sources")} onclick={() => (tab = "sources")}>Sources <span class="text-xs text-black-700">{sources.length + (official ? 1 : 0)}</span></button>
    <button class={tabClass(tab === "available")} onclick={() => (tab = "available")}>Available <span class="text-xs text-black-700">{available.length + officialAvailable.length}</span></button>
    {#if isAdmin}
      <button class={tabClass(tab === "add")} onclick={() => { tab = "add"; editing = null; form = blankForm(); }}>Add new plugin</button>
    {/if}
  </div>

  {#if loading}
    <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if tab === "installed"}
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-black-800 dark:text-black-600" data-testid="installed-summary">
        {counts.total} installed · {counts.active} active · {counts.official} official · {counts.total - counts.official} self-installed
      </p>
      {#if isAdmin}
        <Button variant="secondary" size="sm" onclick={() => checkUpdates()} disabled={busy !== ""}>{busy === "check:all" ? "Checking…" : "Check updates"}</Button>
      {/if}
    </div>
    <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
      <div class="flex flex-wrap items-center gap-1.5" role="group" aria-label="Kind">
        <span class="text-xs text-black-700 dark:text-black-600">Kind</span>
        {#each kinds as k (k)}
          <button class={pillClass(kindFilter === k)} aria-pressed={kindFilter === k} onclick={() => (kindFilter = k)}>{filterLabel(k)}</button>
        {/each}
      </div>
      <div class="flex flex-wrap items-center gap-1.5" role="group" aria-label="Origin">
        <span class="text-xs text-black-700 dark:text-black-600">Origin</span>
        {#each origins as o (o)}
          <button class={pillClass(originFilter === o)} aria-pressed={originFilter === o} onclick={() => (originFilter = o)}>{filterLabel(o)}</button>
        {/each}
      </div>
    </div>
    <div class="divide-y divide-white-300 dark:divide-navy-600 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
      {#each shown as p (rowID(p))}
        <div class="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-start sm:justify-between" data-testid="installed-row">
          <div class="min-w-0 space-y-1">
            <div class="flex flex-wrap items-center gap-2">
              <button class="font-medium text-black-900 hover:underline dark:text-white-100" onclick={() => push(p.detail_path)}>{p.name || p.key}</button>
              <span class="font-mono text-xs text-black-700 dark:text-black-600">{p.key}</span>
              <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">{p.kind} v{p.version}</span>
              <span class="rounded-full px-2 py-0.5 text-[10px] font-medium {originClass[p.origin]}" data-testid="origin-badge">{originLabel(p)}</span>
              {#if p.enabled}
                <span class="rounded-full bg-green-200 dark:bg-green-800 px-2 py-0.5 text-[10px] font-medium text-green-700 dark:text-green-300">Active</span>
              {:else}
                <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">Disabled</span>
              {/if}
            </div>
            <p class="text-xs text-black-700 dark:text-black-600">
              {#if p.update_available}
                <span class="font-medium text-amber-700 dark:text-amber-300">Update available v{p.latest_version}</span>
              {:else if p.origin === "upload" || p.origin === "local"}
                No update source
              {:else}
                <span class="text-green-700 dark:text-green-300">Up to date</span>
              {/if}
              {#if p.origin !== "upload" && p.origin !== "local"} · last check {fmt(p.last_check_at)}{/if}
              {#if p.last_health_at}
                · health <span class={p.last_health_ok ? "text-green-700 dark:text-green-300" : "text-red-600 dark:text-red-400"}>{p.last_health_ok ? "ok" : "failing"}</span> {fmt(p.last_health_at)}
                {#if !p.last_health_ok && p.last_health_detail}<span class="text-red-600 dark:text-red-400"> · {p.last_health_detail}</span>{/if}
              {/if}
            </p>
            {#if p.source_url || p.download_url}
              <p class="flex flex-wrap gap-x-3 text-xs">
                {#if p.source_url}<a class="text-green-700 hover:underline dark:text-green-300" href={p.source_url} target="_blank" rel="noopener noreferrer">{p.origin === "official" ? "Catalog" : "Source"} ↗</a>{/if}
                {#if p.download_url}<a class="text-green-700 hover:underline dark:text-green-300" href={p.download_url} target="_blank" rel="noopener noreferrer">Download ↗</a>{/if}
              </p>
            {/if}
            {#if progress[rowID(p)]}
              <PluginUpdateProgress class="mt-2 max-w-xs" progress={progress[rowID(p)]} />
            {/if}
          </div>
          {#if isAdmin}
            <div class="flex flex-shrink-0 items-center gap-2">
              {#if p.source_id && (p.origin === "source" || p.origin === "url-zip")}
                <Button variant="secondary" size="sm" onclick={() => checkUpdates(p.source_id)} disabled={busy !== ""}>{busy === `check:${p.source_id}` ? "Checking…" : "Check update"}</Button>
              {/if}
              {#if canUpdate(p)}
                <Button size="sm" onclick={() => update(p)} disabled={busy !== ""}>
                  {busy === `update:${rowID(p)}` ? pluginPhaseLabel(progress[rowID(p)]) : `Update to v${p.latest_version}`}
                </Button>
              {/if}
            </div>
          {/if}
        </div>
      {:else}
        <p class="px-4 py-8 text-center text-sm text-black-700 dark:text-black-600">{installed.length ? "No plugin matches these filters." : "No plugins installed yet."}</p>
      {/each}
    </div>
  {:else if tab === "sources"}
    <div class="space-y-3">
      {#if official}
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
      {/if}
      {#each sources as s (s.id)}
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4" data-testid="source-row">
          <div class="flex items-start justify-between gap-4">
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
              <div class="flex flex-shrink-0 items-center gap-2">
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
  {:else if tab === "available"}
    <div class="divide-y divide-white-300 dark:divide-navy-600 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
      {#each available as a (a.source_id + a.key)}
        <div class="flex items-center justify-between gap-4 px-4 py-3" data-testid="available-row">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="font-medium text-black-900 dark:text-white-100">{a.name}</span>
              <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">{a.kind} v{a.version}</span>
            </div>
            <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">{a.description || a.key} · from {a.source_name}</p>
          </div>
          {#if a.installed_version}
            <span class="text-xs text-black-700 dark:text-black-600">installed v{a.installed_version}</span>
          {:else if !a.arch_ok}
            <span class="text-xs text-black-700 dark:text-black-600">no build for this host</span>
          {:else if isAdmin}
            <Button size="sm" onclick={() => install(a)} disabled={busy !== ""}>{busy === `install:${a.key}` ? "Installing…" : "Install"}</Button>
          {/if}
        </div>
      {:else}
        {#if officialAvailable.length === 0}
          <p class="px-4 py-8 text-center text-sm text-black-700 dark:text-black-600">Nothing offered yet — add a source, then Check now.</p>
        {/if}
      {/each}
      {#each officialAvailable as e (e.key)}
        <div class="flex items-center justify-between gap-4 px-4 py-3" data-testid="available-row">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="font-medium text-black-900 dark:text-white-100">{e.name}</span>
              <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">connector v{e.version}</span>
            </div>
            <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">{e.description || e.key} · from Official wick</p>
          </div>
          {#if !e.arch_ok}
            <span class="text-xs text-black-700 dark:text-black-600">no build for this host</span>
          {:else if isAdmin}
            <Button size="sm" onclick={() => installOfficial(e)} disabled={busy !== ""}>{busy === `install:${e.key}` ? "Installing…" : "Install"}</Button>
          {/if}
        </div>
      {/each}
    </div>
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
        {#if editing}<Button variant="secondary" onclick={() => { editing = null; tab = "sources"; }}>Cancel</Button>{/if}
        {#if method !== "upload"}
          <Button variant="secondary" onclick={testForm} disabled={busy !== ""}>{busy === "test-form" ? "Testing…" : "Test"}</Button>
        {/if}
        <Button onclick={submit} disabled={busy !== ""}>{busy === "save" ? "Saving…" : method === "upload" ? "Upload & install" : editing ? "Save" : "Add source"}</Button>
      </div>
    </div>
  {/if}
</div>
