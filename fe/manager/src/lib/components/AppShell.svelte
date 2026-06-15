<script lang="ts">
  import type { Snippet } from "svelte";
  import { Button, ToastHost } from "@wick-fe/common-ui";

  type Props = {
    breadcrumb?: Snippet;
    children: Snippet;
  };
  let { breadcrumb, children }: Props = $props();

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
