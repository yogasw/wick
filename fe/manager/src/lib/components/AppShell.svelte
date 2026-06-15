<script lang="ts">
  import type { Snippet } from "svelte";
  import { Button, ToastHost } from "@wick-fe/common-ui";
  import { push } from "$lib/router.js";

  type Section = "connectors" | "jobs" | "tools" | "audit";
  type Props = {
    breadcrumb?: Snippet;
    children: Snippet;
    section?: Section;
  };
  let { breadcrumb, children, section = "connectors" }: Props = $props();

  /* Top-level manager sections. Connectors + Audit have SPA index views;
     Jobs + Tools are detail-only here (reached from the home dashboard), so
     they appear as active-state indicators without a target. */
  const navItems: { key: Section; label: string; to?: string }[] = [
    { key: "connectors", label: "Connectors", to: "/" },
    { key: "jobs", label: "Jobs" },
    { key: "tools", label: "Tools" },
    { key: "audit", label: "Audit", to: "/audit" },
  ];

  function navClass(active: boolean): string {
    return active
      ? "text-green-600 font-semibold"
      : "text-black-700 dark:text-black-600 hover:text-green-600";
  }

  let dark = $state(document.documentElement.classList.contains("dark"));

  function toggleDark() {
    dark = !dark;
    document.documentElement.classList.toggle("dark", dark);
    try {
      localStorage.setItem("wick-theme", dark ? "dark" : "light");
    } catch {
      /* storage unavailable (private mode / disabled cookies) */
    }
  }
</script>

<ToastHost />

<div class="min-h-screen flex flex-col">
  <header
    class="flex items-center justify-between gap-4 border-b border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-6 py-3"
  >
    <div class="flex items-center gap-3 min-w-0">
      <span class="font-semibold text-sm text-black-900 dark:text-white-100">Manager</span>
      <nav class="flex items-center gap-3 text-sm" aria-label="Sections">
        {#each navItems as item (item.key)}
          {#if item.to}
            <button type="button" class={navClass(section === item.key)} onclick={() => push(item.to ?? "/")}>{item.label}</button>
          {:else}
            <span class={navClass(section === item.key)} aria-current={section === item.key ? "page" : undefined}>{item.label}</span>
          {/if}
        {/each}
      </nav>
      {#if breadcrumb}
        <span class="text-black-500 dark:text-black-600" aria-hidden="true">/</span>
        <nav class="min-w-0 truncate text-sm text-black-700 dark:text-black-600" aria-label="Breadcrumb">
          {@render breadcrumb()}
        </nav>
      {/if}
    </div>
    <Button
      variant="ghost"
      size="sm"
      title="Toggle dark mode"
      onclick={toggleDark}
    >{dark ? "☀ Light" : "🌙 Dark"}</Button>
  </header>

  <main class="flex-1 p-6">
    {@render children()}
  </main>
</div>
