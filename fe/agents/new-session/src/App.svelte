<script lang="ts">
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { ToastHost } from "@wick-fe/common-ui";
  import { getProviderOptions, getPresetOptions, getProjectOptions, createSession } from "$lib/api/options.js";
  import type { ProviderOption, PresetOption, ProjectOption } from "$lib/api/options.js";

  const appEl = document.getElementById("app");
  const base = appEl?.dataset.base ?? "";

  const FOLDER_ICON = "📁";
  const SELECT_BASE = "rounded-lg border px-2.5 py-1.5 text-xs focus:border-green-500 focus:outline-none cursor-pointer";
  const SELECT_INHERITED = "border-green-400 dark:border-green-700 bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-300 font-semibold";
  const SELECT_NEUTRAL = "border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-900 dark:text-white-100";

  let providers = $state<ProviderOption[]>([]);
  let presets = $state<PresetOption[]>([]);
  let projects = $state<ProjectOption[]>([]);

  let selectedProvider = $state("");
  let selectedPreset = $state("");
  let selectedProject = $state("");
  let message = $state("");
  let files = $state<File[]>([]);
  let submitting = $state(false);
  let notifyOn = $state(false);
  let bellDenied = $state(false);

  let fileInputEl: HTMLInputElement | undefined = $state();
  let textareaEl: HTMLTextAreaElement | undefined = $state();

  const scopedProjectId = new URLSearchParams(window.location.search).get("project") ?? "";

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
        if (providers.length > 0) selectedProvider = providers[0].type;
      }
      if (presetRes.status === "fulfilled") {
        presets = presetRes.value;
      }
      if (projRes.status === "fulfilled") {
        projects = projRes.value;
        if (scopedProjectId) {
          const match = projects.find((p) => p.id === scopedProjectId);
          if (match) selectedProject = match.id;
        }
      }
      textareaEl?.focus();
    })();
    return () => {
      cancelled = true;
    };
  });

  $effect(() => {
    if (typeof Notification === "undefined") return;
    notifyOn = Notification.permission === "granted";
    bellDenied = Notification.permission === "denied";
  });

  const isScoped = $derived(!!scopedProjectId && projects.some((p) => p.id === scopedProjectId));
  const scopedProject = $derived(projects.find((p) => p.id === scopedProjectId));
  const selectClass = $derived(`${SELECT_BASE} ${isScoped ? SELECT_INHERITED : SELECT_NEUTRAL}`);

  function autoResize(el: HTMLTextAreaElement) {
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  }

  function onTextareaInput(e: Event) {
    const el = e.currentTarget as HTMLTextAreaElement;
    message = el.value;
    autoResize(el);
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      void submit();
    }
  }

  function onFileChange(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const added = input.files ? Array.from(input.files) : [];
    if (added.length > 0) files = [...files, ...added];
    input.value = "";
  }

  function removeFile(index: number) {
    files = files.filter((_, i) => i !== index);
  }

  async function handleBellClick() {
    if (typeof Notification === "undefined") return;
    if (notifyOn) {
      notifyOn = false;
      toastOk("Notifications muted");
      return;
    }
    if (Notification.permission === "denied") {
      bellDenied = true;
      toastError("Notifications blocked — enable them in your browser settings");
      return;
    }
    if (Notification.permission === "default") {
      const perm = await Notification.requestPermission();
      if (perm === "granted") {
        notifyOn = true;
        bellDenied = false;
        toastOk("Notifications enabled");
      } else {
        bellDenied = perm === "denied";
        toastError("Notifications blocked — enable them in your browser settings");
      }
      return;
    }
    notifyOn = true;
    bellDenied = false;
    toastOk("Notifications enabled");
  }

  async function submit() {
    if (submitting) return;
    if (!message.trim() && files.length === 0) {
      toastError("Type a message or attach a file to start the session.");
      return;
    }
    submitting = true;
    try {
      const url = await createSession(base, message, files, selectedProvider, selectedPreset, selectedProject);
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

  {#if providers.length === 0}
    <div class="w-full rounded-xl border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-900/20 px-4 py-3">
      <p class="text-sm text-amber-700 dark:text-amber-300">
        No healthy providers found. Configure one in
        <a class="font-medium underline" href={`${base}/providers`}>Providers</a>
        first.
      </p>
    </div>
  {:else}
    <div class="w-full overflow-hidden rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
      {#if files.length > 0}
        <div class="flex flex-wrap gap-2 px-3 pt-3">
          {#each files as f, i (f.name + i)}
            <span class="inline-flex items-center gap-1 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1 text-xs text-black-900 dark:text-white-100">
              <span class="max-w-[160px] truncate">{f.name}</span>
              <button
                type="button"
                aria-label={`Remove ${f.name}`}
                class="shrink-0 text-black-500 hover:text-neg-500 dark:text-black-600 dark:hover:text-neg-400 transition-colors"
                onclick={() => removeFile(i)}
              >{"×"}</button>
            </span>
          {/each}
        </div>
      {/if}

      <textarea
        bind:this={textareaEl}
        value={message}
        oninput={onTextareaInput}
        onkeydown={onKeydown}
        rows={3}
        placeholder="Ask anything... (Shift+Enter for new line)"
        class="block w-full resize-none border-0 bg-transparent px-4 pb-2 pt-3.5 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:outline-none focus:ring-0"
      ></textarea>

      <input
        bind:this={fileInputEl}
        type="file"
        multiple
        class="hidden"
        onchange={onFileChange}
        aria-label="File attachment picker"
      />

      <div class="flex flex-wrap items-center gap-2 border-t border-white-300 dark:border-navy-600 bg-white-200/60 dark:bg-navy-800/40 px-3 py-2">
        <button
          type="button"
          aria-label="Notifications"
          title={notifyOn ? "Mute notifications" : "Subscribe to this session's idle notifications"}
          onclick={handleBellClick}
          class="relative inline-flex h-7 w-7 items-center justify-center rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
        >
          <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path d="M8 2.25c-2.07 0-3.75 1.68-3.75 3.75v2.25L3 9.75v.75h10v-0.75L11.75 8.25V6c0-2.07-1.68-3.75-3.75-3.75z" stroke-linejoin="round"></path>
            <path d="M6.5 12a1.5 1.5 0 0 0 3 0" stroke-linecap="round"></path>
            {#if bellDenied}
              <path d="M3 3l10 10" stroke-linecap="round"></path>
            {/if}
          </svg>
          {#if notifyOn && !bellDenied}
            <span class="absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full bg-green-500 ring-2 ring-white-100 dark:ring-navy-700" aria-hidden="true"></span>
          {/if}
        </button>

        <button
          type="button"
          aria-label="Attach file"
          title="Attach file"
          onclick={() => fileInputEl?.click()}
          class="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors"
        >
          <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 7.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48"></path>
          </svg>
        </button>

        {#if projects.length > 0}
          <label class="sr-only" for="ns-project">Project</label>
          <select id="ns-project" bind:value={selectedProject} class={selectClass}>
            <option value="">{"— no project —"}</option>
            {#each projects as proj (proj.id)}
              <option value={proj.id}>{FOLDER_ICON} {proj.name}</option>
            {/each}
          </select>
        {/if}

        <label class="sr-only" for="ns-provider">Provider</label>
        <select id="ns-provider" bind:value={selectedProvider} class={selectClass}>
          {#each providers as p (p.type)}
            <option value={p.type}>{p.type} {"·"} {p.name}</option>
          {/each}
        </select>

        {#if presets.length > 0}
          <label class="sr-only" for="ns-preset">Preset</label>
          <select id="ns-preset" bind:value={selectedPreset} class={selectClass}>
            <option value="">{"— preset (default) —"}</option>
            {#each presets as pr (pr.name)}
              <option value={pr.name}>{pr.name}</option>
            {/each}
          </select>
        {/if}

        <button
          type="button"
          onclick={submit}
          disabled={submitting}
          class="ml-auto inline-flex items-center gap-1.5 rounded-lg bg-green-500 px-3 py-1.5 text-xs font-medium text-white-100 hover:bg-green-600 active:bg-green-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
        >
          <span>{submitting ? "Sending…" : "Send"}</span>
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
            <path d="M2.5 8h11M9 3.5L13.5 8 9 12.5" stroke-linecap="round" stroke-linejoin="round"></path>
          </svg>
        </button>
      </div>
    </div>

    {#if isScoped}
      <p class="mt-3 text-center text-xs text-black-600 dark:text-black-700">Green dropdowns = inherited from project. Click any to override per-session.</p>
    {:else}
      <p class="mt-3 text-center text-xs text-black-600 dark:text-black-700">Pick a project to auto-prefill provider + preset. Folder follows the project.</p>
    {/if}
  {/if}
</div>
