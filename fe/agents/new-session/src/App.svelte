<script lang="ts">
  import { onMount } from "svelte";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastError } from "@wick-fe/common-stores";
  import { ToastHost } from "@wick-fe/common-ui";
  import { getProviderOptions, getPresetOptions, getProjectOptions, createSession } from "$lib/api/options.js";
  import type { ProviderOption, PresetOption, ProjectOption } from "$lib/api/options.js";

  const appEl = document.getElementById("app");
  const base = appEl?.dataset.base ?? "";

  let providers = $state<ProviderOption[]>([]);
  let presets = $state<PresetOption[]>([]);
  let projects = $state<ProjectOption[]>([]);

  let selectedProvider = $state("");
  let selectedPreset = $state("");
  let selectedProject = $state("");
  let message = $state("");
  let files = $state<File[]>([]);
  let submitting = $state(false);

  let fileInputEl: HTMLInputElement | undefined = $state();
  let textareaEl: HTMLTextAreaElement | undefined = $state();

  const scopedProjectId = new URLSearchParams(window.location.search).get("project") ?? "";

  onMount(async () => {
    const [provRes, presetRes, projRes] = await Promise.allSettled([
      Effect.runPromise(getProviderOptions(base).pipe(Effect.provide(WickClientLayer))),
      Effect.runPromise(getPresetOptions(base).pipe(Effect.provide(WickClientLayer))),
      Effect.runPromise(getProjectOptions(base).pipe(Effect.provide(WickClientLayer))),
    ]);

    if (provRes.status === "fulfilled") {
      providers = provRes.value;
      if (providers.length > 0) selectedProvider = providers[0].type;
    }
    if (presetRes.status === "fulfilled") {
      presets = presetRes.value;
      if (presets.length > 0) selectedPreset = presets[0].name;
    }
    if (projRes.status === "fulfilled") {
      projects = projRes.value;
      if (scopedProjectId) {
        const match = projects.find((p) => p.id === scopedProjectId);
        if (match) selectedProject = match.id;
      }
    }

    textareaEl?.focus();
  });

  const isScoped = $derived(!!scopedProjectId && projects.some((p) => p.id === scopedProjectId));
  const scopedProject = $derived(projects.find((p) => p.id === scopedProjectId));

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
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void submit();
    }
  }

  function onFileChange(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    files = input.files ? Array.from(input.files) : [];
  }

  function removeFile(index: number) {
    files = files.filter((_, i) => i !== index);
    if (fileInputEl) fileInputEl.value = "";
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
    <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-green-500 text-xl text-white-100 font-semibold select-none">✦</div>
    {#if isScoped && scopedProject}
      <h1 class="text-2xl font-semibold text-black-900 dark:text-white-100">New session in {scopedProject.name}</h1>
      <p class="mt-1.5 text-sm text-black-700 dark:text-black-600">Provider + preset inherited from the project. Override anything per-session.</p>
    {:else}
      <h1 class="text-2xl font-semibold text-black-900 dark:text-white-100">New session</h1>
      <p class="mt-1.5 text-sm text-black-700 dark:text-black-600">Pick a project (or skip for unscoped). The session is created when you send the first message.</p>
    {/if}
  </div>

  <div class="w-full rounded-2xl border border-black-300 dark:border-black-700 bg-white-100 dark:bg-navy-700 shadow-sm">
    {#if providers.length > 0}
      <div class="flex gap-2 border-b border-black-200 dark:border-black-700 px-4 py-3">
        <select
          bind:value={selectedProvider}
          class={[
            "flex-1 rounded-lg border px-3 py-1.5 text-sm bg-white-100 dark:bg-navy-800 text-black-900 dark:text-white-100 outline-none focus:ring-2 focus:ring-green-500/40",
            isScoped
              ? "border-green-400 dark:border-green-600"
              : "border-black-300 dark:border-black-600",
          ].join(" ")}
        >
          {#each providers as p (p.type)}
            <option value={p.type}>{p.name}</option>
          {/each}
        </select>

        {#if presets.length > 0}
          <select
            bind:value={selectedPreset}
            class={[
              "flex-1 rounded-lg border px-3 py-1.5 text-sm bg-white-100 dark:bg-navy-800 text-black-900 dark:text-white-100 outline-none focus:ring-2 focus:ring-green-500/40",
              isScoped
                ? "border-green-400 dark:border-green-600"
                : "border-black-300 dark:border-black-600",
            ].join(" ")}
          >
            {#each presets as pr (pr.name)}
              <option value={pr.name}>{pr.name}</option>
            {/each}
          </select>
        {/if}

        <select
          bind:value={selectedProject}
          class="flex-1 rounded-lg border border-black-300 dark:border-black-600 px-3 py-1.5 text-sm bg-white-100 dark:bg-navy-800 text-black-900 dark:text-white-100 outline-none focus:ring-2 focus:ring-green-500/40"
        >
          <option value="">— no project —</option>
          {#each projects as proj (proj.id)}
            <option value={proj.id}>{proj.name}</option>
          {/each}
        </select>
      </div>
    {/if}

    <textarea
      bind:this={textareaEl}
      value={message}
      oninput={onTextareaInput}
      onkeydown={onKeydown}
      rows={4}
      placeholder="Type your first message… (Enter to send, Shift+Enter for newline)"
      class="w-full resize-none rounded-t-2xl bg-transparent px-4 py-3 text-sm text-black-900 dark:text-white-100 placeholder-black-500 dark:placeholder-black-600 outline-none"
    ></textarea>

    {#if files.length > 0}
      <div class="flex flex-wrap gap-2 px-4 pb-2">
        {#each files as f, i (f.name + i)}
          <span class="flex items-center gap-1 rounded-full bg-black-100 dark:bg-navy-600 px-2.5 py-0.5 text-xs text-black-700 dark:text-black-400">
            {f.name}
            <button
              type="button"
              onclick={() => removeFile(i)}
              class="ml-0.5 text-black-500 hover:text-neg-500 transition-colors"
              aria-label="Remove file"
            >✕</button>
          </span>
        {/each}
      </div>
    {/if}

    <div class="flex items-center justify-between border-t border-black-200 dark:border-black-700 px-4 py-2">
      <label class="cursor-pointer text-xs text-black-600 dark:text-black-500 hover:text-black-900 dark:hover:text-white-100 transition-colors">
        <input
          bind:this={fileInputEl}
          type="file"
          multiple
          onchange={onFileChange}
          class="sr-only"
        />
        Attach files
      </label>

      <button
        type="button"
        onclick={submit}
        disabled={submitting}
        class="rounded-lg bg-green-500 px-4 py-1.5 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        {#if submitting}Sending…{:else}Send{/if}
      </button>
    </div>
  </div>

  {#if providers.length > 0}
    {#if isScoped}
      <p class="mt-3 text-center text-xs text-black-600 dark:text-black-700">Green dropdowns = inherited from project. Click any to override per-session.</p>
    {:else}
      <p class="mt-3 text-center text-xs text-black-600 dark:text-black-700">Pick a project to auto-prefill provider + preset. Folder follows the project.</p>
    {/if}
  {/if}
</div>
