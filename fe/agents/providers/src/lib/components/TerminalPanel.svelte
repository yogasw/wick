<script lang="ts">
  /* TerminalPanel — the Detail page's "Terminal" section for omp/opencode:
     pick an allowlisted command, open it in a gotty web terminal inside a
     modal. gotty is a wick-managed binary downloaded from this section's
     Binary panel. Closing the modal kills the terminal. Admin-only. */
  import { onMount } from "svelte";
  import { Modal, Button } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import CollapsibleSection from "$lib/components/CollapsibleSection.svelte";
  import ManagedBinaryPanel from "$lib/components/ManagedBinaryPanel.svelte";
  import type { ManagedBinary } from "$lib/managedbin.js";
  import {
    apiTerminalClose,
    apiTerminalStart,
    apiTerminalStatus,
    type TerminalCommand,
    type TerminalSession,
  } from "$lib/terminal.js";

  type Props = { base: string; type: string; name: string };
  let { base, type, name }: Props = $props();

  let commands = $state<TerminalCommand[]>([]);
  let gottyInstalled = $state(false);
  let selected = $state("");
  let starting = $state(false);
  let session = $state<TerminalSession | null>(null);
  let unavailable = $state("");

  async function load() {
    try {
      const s = await apiTerminalStatus(base, type, name);
      commands = s.commands;
      gottyInstalled = s.gottyInstalled;
      if (!commands.some((c) => c.key === selected)) selected = commands[0]?.key ?? "";
      unavailable = "";
    } catch (e) {
      unavailable = e instanceof Error ? e.message : String(e);
    }
  }
  onMount(() => void load());

  function onGotty(m: ManagedBinary | null) {
    const installed = !!m?.current;
    if (installed !== gottyInstalled) void load();
  }

  async function open() {
    if (!selected || starting) return;
    starting = true;
    try {
      session = await apiTerminalStart(base, type, name, selected);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Could not open terminal");
    } finally {
      starting = false;
    }
  }

  function close() {
    const s = session;
    session = null;
    if (s) void apiTerminalClose(base, type, name, s.id).catch(() => {});
  }
</script>

<CollapsibleSection title="Terminal" storageKey="detail.terminal" testid="section-terminal">
  {#snippet summary()}{gottyInstalled ? "gotty ready" : "gotty not installed"}{/snippet}
  {#if unavailable}
    <p class="text-xs text-black-700 dark:text-black-600">{unavailable}</p>
  {:else}
    <p class="text-xs text-black-700 dark:text-black-600">
      Runs one command in this instance's environment, in a terminal on the wick host. Closing the window ends it.
    </p>
    <div class="flex flex-wrap items-center gap-2">
      <select
        data-testid="terminal-command"
        bind:value={selected}
        class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-xs font-mono text-black-900 dark:text-white-100"
      >
        {#each commands as c (c.key)}
          <option value={c.key}>{c.label}</option>
        {/each}
      </select>
      <Button testid="open-terminal" variant="primary" disabled={!gottyInstalled || !selected || starting} onclick={open}>
        {starting ? "Opening…" : "Open terminal"}
      </Button>
    </div>
    <ManagedBinaryPanel {base} type="gotty" compact onChange={onGotty} />
  {/if}
</CollapsibleSection>

<Modal open={session !== null} title={session ? session.command : ""} size="2xl" closeOnBackdrop={false} onClose={close}>
  {#if session}
    <div data-testid="terminal-modal" class="h-[70vh]">
      <iframe title="terminal" src={session.url} class="h-full w-full rounded-lg border border-white-300 dark:border-navy-600 bg-black"></iframe>
    </div>
  {/if}
</Modal>
