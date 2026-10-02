<script lang="ts">
  /* + Agent: the minimum to create one — who it is and which provider runs
     it. Access stays empty (default deny) until the owner ticks connectors
     in Settings → Akses afterwards. */
  import { onMount } from "svelte";
  import { ProviderPicker, buildProviderOptions } from "@wick-fe/common-ui";
  import DrawerHeader from "./DrawerHeader.svelte";
  import { getProviderOptions } from "../api/options.js";
  import { createAgent, runApi, type AgentItem } from "../api/personas.js";
  import { HANDLE_RE, slugHandle, splitPick } from "../agentForm.js";

  type Props = { base: string; onClose: () => void; onCreated: (a: AgentItem) => void };
  let { base, onClose, onCreated }: Props = $props();

  let name = $state("");
  let handle = $state("");
  let handleTouched = $state(false);
  let icon = $state("🤖");
  let description = $state("");
  let systemPrompt = $state("");
  let pick = $state("");
  let providers = $state<{ type: string; name: string; models?: { id: string; label: string; default: boolean }[] }[]>([]);
  let saving = $state(false);
  let error = $state("");

  onMount(() => {
    runApi(getProviderOptions(base)).then((p) => { providers = p; }).catch(() => {});
  });

  // The handle follows the name until the user edits it directly.
  $effect(() => {
    if (!handleTouched) handle = slugHandle(name);
  });

  const handleOk = $derived(HANDLE_RE.test(handle));
  const canSave = $derived(name.trim() !== "" && handleOk && !saving);

  async function submit(e: Event) {
    e.preventDefault();
    if (!canSave) return;
    saving = true;
    error = "";
    const { provider, model } = splitPick(pick);
    try {
      const a = await runApi(
        createAgent(base, {
          handle, name: name.trim(), icon: icon.trim(), description,
          system_prompt: systemPrompt, provider, model,
        }),
      );
      onCreated(a);
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
</script>

<DrawerHeader title="Agent baru" subtitle="Akses connector diatur setelah dibuat" {onClose} />
<form class="flex min-h-0 flex-1 flex-col" onsubmit={submit}>
  <div class="flex-1 space-y-4 overflow-y-auto px-6 py-4">
    <div class="flex gap-3">
      <div class="w-20">
        <label class={label} for="aw-icon">Icon</label>
        <input id="aw-icon" class={input} bind:value={icon} maxlength="8" />
      </div>
      <div class="flex-1">
        <label class={label} for="aw-name">Nama</label>
        <input id="aw-name" class={input} bind:value={name} placeholder="Log Hunter" />
      </div>
    </div>
    <div>
      <label class={label} for="aw-handle">Handle</label>
      <input
        id="aw-handle"
        class={input}
        value={handle}
        oninput={(e) => { handleTouched = true; handle = (e.currentTarget as HTMLInputElement).value.toLowerCase(); }}
        placeholder="log-hunter"
      />
      {#if handle && !handleOk}
        <p class="mt-1 text-xs text-neg-400">Huruf kecil, angka, dan "-", 2–31 karakter, diawali huruf/angka.</p>
      {/if}
    </div>
    <div>
      <label class={label} for="aw-desc">Deskripsi</label>
      <input id="aw-desc" class={input} bind:value={description} placeholder="Apa tugas agent ini" />
    </div>
    <div>
      <label class={label} for="aw-sys">System prompt (persona)</label>
      <textarea id="aw-sys" class="{input} min-h-32" rows="6" bind:value={systemPrompt}></textarea>
    </div>
    <div>
      <span class={label}>Provider</span>
      <ProviderPicker options={buildProviderOptions(providers, pick)} value={pick} onChange={(v) => (pick = v)} placeholder="Default project" />
    </div>
    {#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
  </div>
  <div class="flex justify-end gap-2 border-t border-white-300 px-6 py-4 dark:border-navy-600">
    <button type="button" class="rounded-lg px-4 py-2 text-sm text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600" onclick={onClose}>Batal</button>
    <button
      type="submit"
      class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50"
      disabled={!canSave}
    >{saving ? "Membuat…" : "Buat"}</button>
  </div>
</form>
