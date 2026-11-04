<script lang="ts">
  /* Root for /admin/audit: the audit views inside the admin chrome, with
     the toast host AppShell would otherwise supply. ?tab= picks the view
     (codemode | calls) so a link lands on the right one. */
  import { ToastHost } from "@wick-fe/common-ui";
  import AuditLog from "./AuditLog.svelte";
  import CodeRuns from "./CodeRuns.svelte";

  type Tab = "codemode" | "calls";
  const tabs: { key: Tab; label: string }[] = [
    { key: "codemode", label: "Code mode runs" },
    { key: "calls", label: "Connector calls" },
  ];

  function tabFromUrl(): Tab {
    return new URLSearchParams(window.location.search).get("tab") === "calls" ? "calls" : "codemode";
  }

  let tab = $state<Tab>(tabFromUrl());

  /* Switching drops the other view's filters: they do not share keys. */
  function select(next: Tab): void {
    if (next === tab) return;
    history.replaceState({}, "", `${window.location.pathname}?tab=${next}`);
    tab = next;
  }
</script>

<ToastHost />
<nav class="mb-6 flex gap-1 border-b border-white-300 dark:border-navy-600 text-sm">
  {#each tabs as t (t.key)}
    <button
      type="button"
      class="-mb-px border-b-2 px-4 py-2 {tab === t.key ? 'border-green-600 font-medium text-black-900 dark:text-white-100' : 'border-transparent text-black-800 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100'}"
      onclick={() => select(t.key)}
    >{t.label}</button>
  {/each}
</nav>
{#key tab}
  {#if tab === "codemode"}
    <CodeRuns />
  {:else}
    <AuditLog embedded />
  {/if}
{/key}
