<script lang="ts">
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastError } from "@wick-fe/common-stores";
  import { ToastHost, Composer } from "@wick-fe/common-ui";
  import { getProviderOptions, getPresetOptions, getProjectOptions, createSession } from "$lib/api/options.js";
  import type { ProviderOption, PresetOption, ProjectOption } from "$lib/api/options.js";

  const appEl = document.getElementById("app");
  const base = appEl?.dataset.base ?? "";

  const FOLDER_ICON = "📁";

  let providers = $state<ProviderOption[]>([]);
  let presets = $state<PresetOption[]>([]);
  let projects = $state<ProjectOption[]>([]);
  // Distinguish "still fetching" from "fetched, genuinely empty" so the
  // "No healthy providers" banner doesn't flash on first paint.
  let loadingProviders = $state(true);

  let selectedProvider = $state("");
  let selectedPreset = $state("");
  let selectedProject = $state("");
  let submitting = $state(false);

  const scopedProjectId = new URLSearchParams(window.location.search).get("project") ?? "";

  // The provider key stored in agents.json / project defaults is "type/name".
  function providerKey(p: ProviderOption): string {
    return `${p.type}/${p.name}`;
  }

  // Apply a project's saved defaults to the selects when the user picks a
  // project (and on initial scoped load). Only overrides when the project has a
  // non-empty, still-selectable default; otherwise the current pick stands.
  function applyProjectDefaults(projectId: string) {
    const proj = projects.find((p) => p.id === projectId);
    if (!proj) return;
    if (proj.default_provider) {
      const key = proj.default_provider.includes("/")
        ? proj.default_provider
        : `${proj.default_provider}/${proj.default_provider}`;
      if (providers.some((p) => providerKey(p) === key)) selectedProvider = key;
    }
    if (proj.default_preset) {
      selectedPreset = proj.default_preset === "default" ? "" : proj.default_preset;
    }
  }

  $effect(() => {
    let cancelled = false;
    (async () => {
      const [provRes, presetRes, projRes] = await Promise.allSettled([
        Effect.runPromise(getProviderOptions(base).pipe(Effect.provide(WickClientLayer))),
        Effect.runPromise(getPresetOptions(base).pipe(Effect.provide(WickClientLayer))),
        Effect.runPromise(getProjectOptions(base).pipe(Effect.provide(WickClientLayer))),
      ]);
      if (cancelled) return;
      if (provRes.status === "fulfilled") {
        providers = provRes.value;
        if (providers.length > 0) selectedProvider = providerKey(providers[0]);
      }
      loadingProviders = false;
      if (presetRes.status === "fulfilled") presets = presetRes.value;
      if (projRes.status === "fulfilled") {
        projects = projRes.value;
        if (scopedProjectId) {
          const match = projects.find((p) => p.id === scopedProjectId);
          if (match) {
            selectedProject = match.id;
            applyProjectDefaults(match.id);
          }
        }
      }
    })();
    return () => { cancelled = true; };
  });

  const isScoped = $derived(!!scopedProjectId && projects.some((p) => p.id === scopedProjectId));
  const scopedProject = $derived(projects.find((p) => p.id === scopedProjectId));

  const projectOptions = $derived([
    { label: "— no project —", value: "" },
    ...projects.map((p) => ({ label: `${FOLDER_ICON} ${p.name}`, value: p.id })),
  ]);
  const providerOptions = $derived(
    providers.map((p) => ({
      label: p.name === p.type ? p.type : `${p.type} · ${p.name}`,
      value: providerKey(p),
    })),
  );
  const presetOptions = $derived([
    { label: "— preset (default) —", value: "" },
    ...presets.map((pr) => ({ label: pr.name, value: pr.name })),
  ]);

  const projectSelect = $derived(
    projects.length > 0
      ? { options: projectOptions, value: selectedProject, onChange: (v: string) => { selectedProject = v; applyProjectDefaults(v); } }
      : undefined,
  );
  const providerSelect = $derived({ options: providerOptions, value: selectedProvider, onChange: (v: string) => (selectedProvider = v) });
  const presetSelect = $derived(
    presets.length > 0
      ? { options: presetOptions, value: selectedPreset, onChange: (v: string) => (selectedPreset = v) }
      : undefined,
  );

  async function handleSend({ text, files }: { text: string; files: File[] }) {
    if (submitting) return;
    if (!text.trim() && files.length === 0) {
      toastError("Type a message or attach a file to start the session.");
      return;
    }
    submitting = true;
    try {
      const url = await createSession(base, text, files, selectedProvider, selectedPreset, selectedProject);
      window.location.href = url;
    } catch (err) {
      toastError(err instanceof Error ? err.message : "Failed to create session.");
      submitting = false;
    }
  }
</script>

<ToastHost />

<div class="mx-auto flex h-full w-full max-w-2xl flex-col items-center justify-center px-6">
  <div class="mb-8 text-center">
    <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-green-500 text-xl text-white-100 font-semibold select-none">{"✦"}</div>
    {#if isScoped && scopedProject}
      <h1 class="text-2xl font-semibold text-black-900 dark:text-white-100">New session in {FOLDER_ICON} {scopedProject.name}</h1>
      <p class="mt-1.5 text-sm text-black-700 dark:text-black-600">Provider + preset inherited from the project. Override anything per-session.</p>
    {:else}
      <h1 class="text-2xl font-semibold text-black-900 dark:text-white-100">New session</h1>
      <p class="mt-1.5 text-sm text-black-700 dark:text-black-600">Pick a project (or skip for unscoped). The session is created when you send the first message.</p>
    {/if}
  </div>

  {#if loadingProviders}
    <div class="h-[52px] w-full"></div>
  {:else if providers.length === 0}
    <div class="w-full rounded-xl border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-900/20 px-4 py-3">
      <p class="text-sm text-amber-700 dark:text-amber-300">
        No healthy providers found. Configure one in
        <a class="font-medium underline" href={`${base}/providers`}>Providers</a>
        first.
      </p>
    </div>
  {:else}
    <div class="w-full">
      <Composer
        onSend={handleSend}
        disabled={submitting}
        minRows={3}
        requireContent={false}
        placeholder="Ask anything... (Shift+Enter for new line)"
        submitLabel={submitting ? "Sending…" : "Send"}
        notifyKey="wick.newsession.notify"
        project={projectSelect}
        provider={providerSelect}
        preset={presetSelect}
      />
    </div>

    {#if isScoped}
      <p class="mt-3 text-center text-xs text-black-600 dark:text-black-700">Green dropdowns = inherited from project. Click any to override per-session.</p>
    {:else}
      <p class="mt-3 text-center text-xs text-black-600 dark:text-black-700">Pick a project to auto-prefill provider + preset. Folder follows the project.</p>
    {/if}
  {/if}
</div>
