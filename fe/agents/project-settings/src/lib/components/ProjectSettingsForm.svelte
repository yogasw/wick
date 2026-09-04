<script lang="ts">
  import { ConfirmDialog, ProviderPicker, buildProviderOptions } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import {
    getProjectSettings,
    updateProject,
    createProject,
    deleteProject,
    unpinSession,
    getProviderOptionModels,
    saveTicketConfig,
  } from "$lib/api.js";
  import type { ProjectSettingsData, WidgetPolicy, TicketConfig } from "$lib/types.js";
  import { createAutosave, type SaveStatus } from "$lib/autosave.js";
  import WidgetPolicyEditor from "./WidgetPolicyEditor.svelte";
  import TicketSystemEditor from "./TicketSystemEditor.svelte";
  import SettingsSection from "./SettingsSection.svelte";

  type Props = {
    projectID: string;
    base: string;
    /* The shell renders the save indicator next to the tab strip, so the
       form reports status upward instead of drawing its own. */
    onStatus?: (s: SaveStatus, retry: () => void) => void;
  };
  let { projectID, base, onStatus }: Props = $props();

  let data = $state<ProjectSettingsData | null>(null);
  let loading = $state(true);
  let error = $state("");
  let creating = $state(false);
  let showDeleteConfirm = $state(false);

  let name = $state("");
  let icon = $state("");
  let description = $state("");
  let folderMode = $state<"managed" | "custom">("managed");
  let customPath = $state("");
  let preset = $state("default");
  let systemAddon = $state("");

  // The widget CSP override. The allowlist is held as raw textarea text, not
  // a parsed array: the server validates it and names the offending line
  // back, which it can only do if the operator's own text survives the trip.
  let widget = $state<WidgetPolicy>({});
  let widgetAllowlistText = $state("");

  // Ticket-mode config, persisted through its own endpoint.
  let ticketCfg = $state<TicketConfig>({});

  // Promote a bare provider type ("claude") to its canonical default
  // instance key ("claude/claude"). Mirrors normalizeProviderKey on the
  // backend so the dropdown value round-trips to the spawn path. Empty
  // stays empty.
  function normalizeProviderKey(key: string): string {
    if (!key) return "";
    return key.includes("/") ? key : `${key}/${key}`;
  }

  // Provider and model are two stored fields but ONE choice, so the picker
  // owns them as its packed "type/name::modelID" value and they are split
  // apart only on save. Holding them separately here would let a save pair
  // a provider with a model belonging to a different instance.
  let pickerValue = $state("");
  const providerKey = $derived(pickerValue.split("::")[0] ?? "");
  const modelID = $derived.by(() => {
    const i = pickerValue.indexOf("::");
    return i < 0 ? "" : pickerValue.slice(i + 2);
  });

  // Options come from the shared builder: the sub-agent role editor renders
  // the same list, and two copies of this mapping would drift.
  let providerOptions = $derived(buildProviderOptions(data?.provider_list ?? [], pickerValue));

  // Lazy model loader for the picker's 3rd/4th levels. wick's live sets are
  // resolved against the vendor on demand — a build-time list would go stale
  // and could not offer the leaf models a project needs to pin.
  function loadProviderModels(optionValue: string, opts?: { entry?: string }) {
    const slash = optionValue.indexOf("/");
    const type = slash < 0 ? optionValue : optionValue.slice(0, slash);
    const name = slash < 0 ? optionValue : optionValue.slice(slash + 1);
    return getProviderOptionModels(type, name, opts);
  }

  /* ── auto-save ────────────────────────────────────────────────────────
     There is no Save button. Text edits save on a debounce, committing
     interactions (select, toggle, radio, blur) flush immediately, and the
     shell shows a quiet status line.

     Autosave starts suspended so seeding the fields in load() does not
     immediately write them back, and stays suspended for an unsaved new
     project — that one still goes through explicit "Create project",
     because there is no project id to PATCH until it exists. */
  const autosave = createAutosave({
    save: persist,
    suspended: true,
    onStatus: (s) => onStatus?.(s, autosave.retry),
  });

  async function persist() {
    if (!data || data.is_new) return;
    // Folder mode moves the agent cwd, and the server rejects an empty
    // custom path. Holding the request back until the path is filled keeps
    // a half-typed path from failing on every keystroke.
    const path = folderMode === "managed" ? "" : customPath.trim();
    if (folderMode === "custom" && path === "") return;

    await updateProject(projectID, {
      name: name.trim(),
      icon: icon.trim(),
      description,
      folder_mode: folderMode,
      custom_path: path,
      preset,
      provider: providerKey,
      model: modelID,
      system_addon: systemAddon,
      widget: widgetPayload(),
    });
    // Second endpoint, same save cycle: the status only reports success
    // once both have landed, so "Saved" never overstates what was stored.
    await saveTicketConfig(projectID, ticketCfg);
  }

  /* Bound to text inputs — waits for a pause in typing. */
  const edit = () => autosave.schedule();
  /* Bound to blur — saves at once, but only if an edit is pending, so
     tabbing through an untouched form stays silent. */
  const commit = () => autosave.flush();
  /* Bound to selects, pickers, toggles, radios — the event itself is the
     edit, so this saves unconditionally. flush() would skip it: nothing
     marked the form dirty beforehand. */
  const change = () => autosave.commit();

  async function load() {
    loading = true;
    error = "";
    autosave.setSuspended(true);
    try {
      const d = await getProjectSettings(projectID);
      data = d;
      name = d.name;
      icon = d.icon;
      description = d.description;
      folderMode = d.managed ? "managed" : "custom";
      customPath = d.custom_path;
      preset = d.default_preset;
      // Normalize to the "type/name" key the spawn path expects. Older
      // projects stored a bare type (e.g. "claude"); promote it to the
      // canonical default instance "claude/claude". Empty stays empty so
      // the dropdown falls back to the first available instance.
      //
      // Repacked into the picker's single value, with the model only when a
      // provider exists to resolve it against.
      const key = normalizeProviderKey(d.default_provider);
      pickerValue = key && d.default_model ? `${key}::${d.default_model}` : key;
      systemAddon = d.system_addon;
      widget = d.widget ?? {};
      widgetAllowlistText = (d.widget?.allowlist ?? []).join("\n");
      ticketCfg = d.ticket ?? {};
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
      // An existing project auto-saves from here on; a new one waits for
      // the explicit create.
      autosave.setSuspended(data?.is_new !== false);
    }
  }

  /* Build the widget override for submit.

     With the override switched off, everything is sent at its zero value
     rather than at whatever was last typed. Sending stale settings alongside
     override:false would persist permissions the operator turned off, so
     switching the override back on later would silently restore ones they
     believed they had abandoned.

     With the override ON, the per-directive detail is sent even under the
     secure/unsecure presets. It is inert there — the backend expands the
     preset and ignores these fields — but persisting it is what lets a
     Custom setup survive a trip through Secure and back. */
  function widgetPayload() {
    if (widget.override !== true) {
      return {
        override: false,
        mode: "secure",
        frame_src: "block",
        img_src: "block",
        media_src: "block",
        connect_src: "block",
        script_src: "block",
        allow_popups: false,
        allow_popup_escape: false,
        allowlist: "",
      };
    }
    const mode = widget.mode === "unsecure" || widget.mode === "custom" ? widget.mode : "secure";
    return {
      override: true,
      mode,
      frame_src: widget.frame_src ?? "block",
      img_src: widget.img_src ?? "block",
      media_src: widget.media_src ?? "block",
      connect_src: widget.connect_src ?? "block",
      script_src: widget.script_src ?? "block",
      allow_popups: widget.allow_popups === true,
      // Never stored on its own: an escape flag with no popups permitted reads
      // as a permission that is doing something when nothing can open a tab.
      allow_popup_escape: widget.allow_popups === true && widget.allow_popup_escape === true,
      allowlist: widgetAllowlistText,
    };
  }

  async function handleCreate(e: SubmitEvent) {
    e.preventDefault();
    if (!data) return;
    creating = true;
    try {
      const redirectURL = await createProject({
        name: name.trim(),
        icon: icon.trim(),
        description,
        folder_mode: folderMode,
        custom_path: folderMode === "managed" ? "" : customPath.trim(),
        preset,
        provider: providerKey,
        model: modelID,
        system_addon: systemAddon,
      });
      window.location.href = redirectURL;
    } catch (err) {
      toastError("Create failed", err instanceof Error ? err.message : String(err));
      creating = false;
    }
  }

  async function handleDelete() {
    showDeleteConfirm = false;
    try {
      await deleteProject(projectID);
      window.location.href = `${base}/sessions`;
    } catch (err) {
      toastError("Delete failed", err instanceof Error ? err.message : String(err));
    }
  }

  async function handleUnpin(sessionID: string) {
    if (!data) return;
    try {
      await unpinSession(projectID, sessionID);
      await load();
    } catch (err) {
      toastError("Unpin failed", err instanceof Error ? err.message : String(err));
    }
  }

  function backHref(): string {
    if (data && !data.is_new && data.id) {
      return `${base}/sessions?project=${data.id}`;
    }
    return `${base}/sessions`;
  }

  /* Snippets are compiled outside the `{:else if data}` block, so the
     narrowing there does not reach them — these read the flags instead. */
  const canDelete = $derived(data !== null && !data.is_new && !data.is_protected);

  /* Folded sections state their current setting in the subtitle, so the
     page still answers "is this on?" without anything being expanded. */
  const ticketSummary = $derived.by(() => {
    if (ticketCfg.enabled !== true) return "Off — sessions here are plain chats.";
    const cols = (ticketCfg.statuses ?? []).length;
    const parts = [
      cols === 0 ? "default columns" : `${cols} columns`,
      `${(ticketCfg.fields ?? []).length} custom field(s)`,
    ];
    parts.push(
      ticketCfg.followup_after_sec
        ? `follow up after ${Math.round(ticketCfg.followup_after_sec / 60)}m`
        : "no follow-up",
    );
    parts.push(
      ticketCfg.auto_resolve_after_sec
        ? `auto-resolve after ${Math.round(ticketCfg.auto_resolve_after_sec / 86400)}d`
        : "no auto-resolve",
    );
    const rules = (ticketCfg.auto_create ?? []).length;
    parts.push(rules === 0 ? "no auto-create" : `${rules} auto-create rule(s)`);
    // Integrations are worth surfacing in the collapsed header: an endpoint
    // receiving events, or an open API, is not something to discover by
    // expanding a sub-section.
    const hooks = (ticketCfg.integrations?.webhooks ?? []).filter((w) => w.enabled).length;
    if (hooks > 0) parts.push(`${hooks} webhook(s)`);
    if (ticketCfg.integrations?.api_enabled === true) parts.push("REST API on");
    return `On — ${parts.join(" · ")}.`;
  });

  const widgetSummary = $derived(
    widget.override === true
      ? `This project overrides the global policy — ${widget.mode ?? "secure"} mode.`
      : "Following the global policy.",
  );

  const folderModeClass = (active: boolean) =>
    active
      ? "flex-1 rounded-lg bg-green-500 px-4 py-2 text-xs font-semibold text-white-100 transition-colors"
      : "flex-1 rounded-lg px-4 py-2 text-xs font-medium text-black-800 transition-colors hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600";

  $effect(() => { load(); });
  $effect(() => () => autosave.dispose());
</script>

<ConfirmDialog
  open={showDeleteConfirm}
  title="Delete project?"
  body="All sessions in this project will be moved to the default project. This cannot be undone."
  confirmLabel="Delete"
  destructive={true}
  onConfirm={handleDelete}
  onCancel={() => { showDeleteConfirm = false; }}
/>

{#if loading}
  <div class="py-16 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if error}
  <div class="rounded-xl border border-neg-400/40 bg-neg-100 px-4 py-3 text-sm text-neg-400">{error}</div>
{:else if data}
  <form onsubmit={handleCreate} class="flex flex-col gap-4">
    <!-- Identity. An icon + name pair reads as the page's subject, so it
         sits in its own card above the settings rather than inside them. -->
    <SettingsSection
      title="Project"
      subtitle={data.is_new
        ? "Name it and pick where its sessions run."
        : `${data.chat_count} chats · created ${data.created_at} · ${data.managed ? "managed" : "custom"} folder`}
      collapsible={!data.is_new}
      open={data.is_new}
    >
      {#snippet action()}
        {#if canDelete}
          <button
            type="button"
            onclick={() => { showDeleteConfirm = true; }}
            class="rounded-lg border border-neg-400/40 px-3 py-1.5 text-xs font-medium text-neg-400 transition-colors hover:bg-neg-100"
          >Delete project</button>
        {/if}
      {/snippet}

      <div class="flex items-center gap-4">
        <input
          type="text"
          maxlength="2"
          aria-label="Project icon"
          bind:value={icon}
          oninput={edit}
          onblur={commit}
          class="h-14 w-14 shrink-0 rounded-xl border border-white-300 bg-white-200 text-center text-2xl outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-800"
        />
        <div class="min-w-0 flex-1">
          <label class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600" for="ps-name">Name</label>
          <input
            id="ps-name"
            type="text"
            required
            bind:value={name}
            oninput={edit}
            onblur={commit}
            placeholder="Project name"
            class="w-full rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm font-medium text-black-900 outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
          />
        </div>
      </div>
      <div class="mt-4">
        <label class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600" for="ps-description">Description</label>
        <input
          id="ps-description"
          type="text"
          bind:value={description}
          oninput={edit}
          onblur={commit}
          placeholder="What this project is for"
          class="w-full rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm text-black-900 outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
        />
      </div>
    </SettingsSection>

    <!-- Folder: two mutually exclusive modes, so a segmented control rather
         than radios — it shows the active choice without reading labels. -->
    <SettingsSection
      title="Folder"
      subtitle="Where agent subprocesses run. Changing it shifts the cwd at the next spawn; a running subprocess is unaffected until it restarts."
      collapsible={!data.is_new}
      open={data.is_new}
    >
      <div class="flex gap-1 rounded-xl bg-white-200 p-1 dark:bg-navy-800">
        <button
          type="button"
          aria-pressed={folderMode === "managed"}
          onclick={() => { folderMode = "managed"; change(); }}
          class={folderModeClass(folderMode === "managed")}
        >Managed</button>
        <button
          type="button"
          aria-pressed={folderMode === "custom"}
          onclick={() => { folderMode = "custom"; change(); }}
          class={folderModeClass(folderMode === "custom")}
        >Custom path</button>
      </div>

      {#if folderMode === "managed"}
        <p class="mt-3 text-xs leading-relaxed text-black-700 dark:text-black-600">
          Wick creates and owns <code class="rounded bg-white-200 px-1 font-mono dark:bg-navy-800">projects/&lt;id&gt;/files/</code>.
          Good for scratch sessions. Switching away from managed leaves that folder on disk as an orphaned backup.
        </p>
      {:else}
        <div class="mt-3">
          <div class="flex gap-2">
            <input
              type="text"
              aria-label="Custom folder path"
              bind:value={customPath}
              oninput={edit}
              onblur={commit}
              placeholder="D:/code/work/my-project"
              class="min-w-0 flex-1 rounded-lg border border-white-400 bg-white-100 px-3 py-2 font-mono text-xs text-black-900 outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
            />
            <label class="shrink-0 cursor-pointer rounded-lg border border-white-400 px-3 py-2 text-xs font-medium text-black-800 transition-colors hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-800">
              Choose…
              <input
                type="file"
                class="hidden"
                onchange={(e) => {
                  const f = (e.currentTarget as HTMLInputElement).files?.[0];
                  if (f) { customPath = f.name; change(); }
                }}
              />
            </label>
          </div>
          <p class="mt-2 text-xs leading-relaxed text-black-700 dark:text-black-600">
            Absolute path to a folder that already exists. Browsers hide absolute paths, so the
            picker fills only the folder name — prefix the parent yourself. Saved once the path is filled.
          </p>
        </div>
      {/if}
    </SettingsSection>

    <SettingsSection
      title="Defaults"
      subtitle="Where new sessions in this project start. Sub-agents inherit the provider and model."
      collapsible={!data.is_new}
      open={data.is_new}
    >
      <div class="flex flex-col gap-4">
        <div>
          <label for="ps-provider" class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600">Provider</label>
          <ProviderPicker
            id="ps-provider"
            options={providerOptions}
            value={pickerValue}
            onChange={(v) => { pickerValue = v; change(); }}
            loadModels={loadProviderModels}
            placeholder="Select provider"
          />
        </div>
        <div>
          <label for="ps-preset" class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600">Preset</label>
          <select
            id="ps-preset"
            bind:value={preset}
            onchange={change}
            class="w-full rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm text-black-900 outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
          >
            {#each data.preset_list as p (p)}
              <option value={p}>{p}</option>
            {/each}
          </select>
        </div>
        <div>
          <label for="ps-addon" class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600">System prompt addon</label>
          <textarea
            id="ps-addon"
            rows="3"
            bind:value={systemAddon}
            oninput={edit}
            onblur={commit}
            placeholder="Appended to the preset system prompt for every session in this project"
            class="w-full rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm text-black-900 outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
          ></textarea>
        </div>
      </div>
    </SettingsSection>

    {#if !data.is_new}
      <!-- Both of these are long editors that most visits do not touch, so
           they stay folded with their current state in the subtitle. -->
      <div onchange={change} role="none">
        <SettingsSection
          title="Ticket system"
          subtitle={ticketSummary}
          collapsible
        >
          <TicketSystemEditor
            {projectID}
            {base}
            cfg={ticketCfg}
            onChange={(c) => { ticketCfg = c; edit(); }}
          />
        </SettingsSection>
      </div>

      <div onchange={change} role="none">
        <SettingsSection
          title="Widget permissions"
          subtitle={widgetSummary}
          collapsible
        >
          <p class="mb-3 text-xs leading-relaxed text-black-700 dark:text-black-600">
            Widget HTML is written by the agent, so it runs sealed off by default. Pick a mode —
            individual permissions only matter under Custom.
          </p>
          <WidgetPolicyEditor
            policy={widget}
            inherited={data.widget_inherited}
            allowlistText={widgetAllowlistText}
            onChange={(next) => { widget = next.policy; widgetAllowlistText = next.allowlistText; edit(); }}
          />
        </SettingsSection>
      </div>

      <SettingsSection
        title="Pinned sessions"
        subtitle={data.pinned.length === 0
          ? "Nothing pinned yet. Pin a chat from its menu."
          : `${data.pinned.length} pinned chat${data.pinned.length === 1 ? "" : "s"}.`}
        collapsible
      >
        {#if data.pinned.length === 0}
          <p class="text-xs text-black-700 dark:text-black-600">Nothing pinned yet.</p>
        {:else}
          <ul class="flex flex-col gap-2">
            {#each data.pinned as pin (pin.id)}
              <li class="flex items-center gap-3 rounded-lg border border-white-300 bg-white-100 px-4 py-2.5 dark:border-navy-600 dark:bg-navy-800">
                <span class="min-w-0 flex-1 truncate text-sm text-black-900 dark:text-white-100">{pin.label}</span>
                <button
                  type="button"
                  aria-label="Unpin session"
                  onclick={() => handleUnpin(pin.id)}
                  class="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-neg-100 hover:text-neg-400 dark:text-black-600"
                >
                  <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                    <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path>
                  </svg>
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </SettingsSection>

      <!-- Collapsed by default: worth having when something looks wrong,
           not worth the vertical space the rest of the time. -->
      <SettingsSection
        title="Advanced"
        subtitle="Raw project meta and folder-change semantics."
        collapsible
      >
        <h3 class="mb-2 text-xs font-semibold text-black-800 dark:text-black-600">Project meta</h3>
        <pre class="overflow-x-auto rounded-lg border border-white-300 bg-white-200 p-3 text-xs text-black-800 dark:border-navy-600 dark:bg-navy-800 dark:text-black-600">{data.meta_json}</pre>
        <h3 class="mb-2 mt-4 text-xs font-semibold text-black-800 dark:text-black-600">Folder change semantics</h3>
        <ul class="list-disc space-y-1 pl-5 text-xs leading-relaxed text-black-700 dark:text-black-600">
          <li>Managed → custom: the managed <code class="font-mono">files/</code> stays on disk as an orphaned backup; delete it manually.</li>
          <li>Custom → managed: a new managed folder is created; the custom path is left untouched.</li>
          <li>Live sessions: the cwd shifts at the next spawn; a running subprocess is unaffected until it restarts.</li>
        </ul>
      </SettingsSection>
    {/if}

    <!-- A project that does not exist yet cannot be auto-saved: there is no
         id to write to until it is created. -->
    {#if data.is_new}
      <div class="flex items-center justify-end gap-3 pb-2">
        <a
          href={backHref()}
          class="rounded-lg border border-white-400 px-4 py-2 text-sm text-black-800 transition-colors hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-800"
        >Cancel</a>
        <button
          type="submit"
          disabled={creating}
          class="rounded-lg bg-green-600 px-5 py-2 text-sm font-semibold text-white-100 transition-colors hover:bg-green-700 active:bg-green-800 disabled:opacity-40"
        >{creating ? "Creating…" : "Create project"}</button>
      </div>
    {/if}
  </form>
{/if}
