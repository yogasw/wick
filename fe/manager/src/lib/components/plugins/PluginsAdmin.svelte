<script lang="ts">
  /* Admin → Plugins. Sources: url / GitHub sources with Test (six-step health
     check), Check now, Edit, Delete. Available: what the sources offer, with
     Install. Add: Upload a zip, Link (plugins.json or a .zip, installed once)
     or GitHub release (public, or private with a PAT that is write-only).
     Everyone logged in can read; actions are admin-only server-side and hidden
     here for non-admins. Updates of installed plugins live on each plugin's
     detail page (kebab), not here. */
  import { Button, TextInput } from "@wick-fe/common-ui";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import {
    listPluginSources, savePluginSource, deletePluginSource, testPluginSource, checkPluginSource,
    listAvailablePlugins, installFromSource, uploadPluginZip,
    type PluginSource, type PluginSourceInput, type SourceStep, type AvailablePlugin,
  } from "$lib/api.js";

  type Tab = "sources" | "available" | "add";
  type Method = "upload" | "link" | "github";
  let tab = $state<Tab>("sources");
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
      const [s, a] = await Promise.all([listPluginSources(), listAvailablePlugins()]);
      sources = s.sources;
      available = a.available;
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

  function edit(s: PluginSource): void {
    editing = s.id;
    method = s.type === "github" ? "github" : "link";
    form = { ...blankForm(), type: s.type, url: s.url ?? "", repo: s.repo ?? "", visibility: s.private ? "private" : "public",
      pub_key: s.pub_key ?? "", key_filter: s.key_filter ?? "", allow_prerelease: s.allow_prerelease, auto_update: s.auto_update, poll_minutes: s.poll_minutes };
    tab = "add";
  }

  const submit = () => run("save", async () => {
    if (method === "upload") {
      if (!file) throw new Error("Choose a .zip first");
      const res = await uploadPluginZip(file);
      toastOk(`Installed ${res.key} (${res.kind}) v${res.version}`);
      file = null;
    } else {
      const input: PluginSourceInput = {
        type: method === "github" ? "github" : "url", url: form.url, repo: form.repo, private: form.visibility === "private",
        pat: form.pat || undefined, pub_key: form.pub_key, key_filter: form.key_filter,
        allow_prerelease: form.allow_prerelease, auto_update: form.auto_update, poll_minutes: form.poll_minutes,
      };
      const saved = await savePluginSource(input, editing ?? undefined);
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

<div class="space-y-5" data-testid="plugins-admin">
  <div>
    <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Plugins</h1>
    <p class="mt-0.5 text-sm text-black-800 dark:text-black-600">Sources wick installs connector, tool, job and service plugins from. Updates of an installed plugin are on its own page.</p>
  </div>

  <div class="flex gap-1 border-b border-white-300 dark:border-navy-600">
    <button class={tabClass(tab === "sources")} onclick={() => (tab = "sources")}>Sources <span class="text-xs text-black-700">{sources.length}</span></button>
    <button class={tabClass(tab === "available")} onclick={() => (tab = "available")}>Available <span class="text-xs text-black-700">{available.length}</span></button>
    {#if isAdmin}
      <button class={tabClass(tab === "add")} onclick={() => { tab = "add"; editing = null; form = blankForm(); }}>Add new plugin</button>
    {/if}
  </div>

  {#if loading}
    <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if tab === "sources"}
    {#if sources.length === 0}
      <p class="rounded-xl border border-dashed border-white-300 dark:border-navy-600 px-4 py-8 text-center text-sm text-black-700 dark:text-black-600">No sources yet.</p>
    {/if}
    <div class="space-y-3">
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
          {#if tests[s.id]}
            <ol class="mt-3 divide-y divide-white-300 dark:divide-navy-600 rounded-lg border border-white-300 dark:border-navy-600" data-testid="source-test">
              {#each tests[s.id] as st (st.n)}
                <li class="flex items-start gap-3 px-3 py-2 text-sm">
                  <span>{icon[st.status]}</span>
                  <span class="w-32 flex-shrink-0 font-medium text-black-900 dark:text-white-100">{st.n}. {st.name}</span>
                  <span class="min-w-0 break-words {st.status === 'fail' ? 'text-red-600 dark:text-red-400' : 'text-black-800 dark:text-black-600'}">{st.message}</span>
                </li>
              {/each}
            </ol>
          {/if}
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
        <p class="px-4 py-8 text-center text-sm text-black-700 dark:text-black-600">Nothing offered yet — add a source, then Check now.</p>
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
        <TextInput label="URL" placeholder="https://example.com/plugins.json" bind:value={form.url} />
        <p class="text-xs text-black-700 dark:text-black-600">A plugins.json URL is polled for updates. A direct .zip link installs once, without updates.</p>
      {:else}
        <TextInput label="Repository" placeholder="owner/repo" bind:value={form.repo} />
        <div class="flex gap-4 text-sm text-black-800 dark:text-black-600">
          <label class="flex items-center gap-1.5"><input type="radio" value="public" bind:group={form.visibility} /> Public</label>
          <label class="flex items-center gap-1.5"><input type="radio" value="private" bind:group={form.visibility} /> Private</label>
        </div>
        {#if form.visibility === "private"}
          <TextInput label="Personal access token" type="password" placeholder={editing ? "•••••••• stored — leave empty to keep" : "github_pat_…"} bind:value={form.pat} />
          <p class="text-xs text-black-700 dark:text-black-600">Fine-grained, this repo only, Contents: Read-only. Stored encrypted, never shown again, sent only to api.github.com.</p>
        {/if}
        <TextInput label="Plugin keys (optional)" placeholder="all plugins in the repo" bind:value={form.key_filter} />
      {/if}

      {#if method !== "upload"}
        <TextInput label="Publisher key (optional)" placeholder="base64 ed25519 — pins signatures" bind:value={form.pub_key} />
        <div class="flex flex-wrap gap-4 text-sm text-black-800 dark:text-black-600">
          {#if method === "github"}<label class="flex items-center gap-1.5"><input type="checkbox" bind:checked={form.allow_prerelease} /> Include prereleases</label>{/if}
          <label class="flex items-center gap-1.5"><input type="checkbox" bind:checked={form.auto_update} /> Auto-update</label>
        </div>
      {/if}

      <div class="flex justify-end gap-2">
        {#if editing}<Button variant="secondary" onclick={() => { editing = null; tab = "sources"; }}>Cancel</Button>{/if}
        <Button onclick={submit} disabled={busy !== ""}>{busy === "save" ? "Saving…" : method === "upload" ? "Upload & install" : editing ? "Save" : "Add source"}</Button>
      </div>
    </div>
  {/if}
</div>
