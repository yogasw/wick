<script lang="ts">
  /* The roster header's account button: the viewer's initial and a
     chevron, opening a dropdown laid out like wick's own account menu
     (nav.templ UserMenu): the name on top, then Team settings, Theme,
     Mini Tools and, past a divider, the way back to the Agents pages.
     Esc or a press outside closes it; Esc gives focus back to the button. */
  import { tick } from "svelte";

  type Props = {
    viewerName: string;
    /** Where "Switch to Agents" goes (teamReturn.ts, marked classic). */
    exitHref: string;
    /** The Light/Dark switch: the mode on screen and the user's paired
        theme ids, posted to wick's /theme. null hides it (signed out). */
    theme: { mode: "light" | "dark"; light: string; dark: string } | null;
    onSettings: () => void;
  };
  let { viewerName, exitHref, theme, onSettings }: Props = $props();

  let open = $state(false);
  let root = $state<HTMLDivElement>();
  let button = $state<HTMLButtonElement>();
  const initial = $derived((viewerName.trim()[0] ?? "?").toUpperCase());
  // /theme redirects back to this page, panel and all.
  const here = () => location.pathname + location.search;

  async function close(refocus: boolean) {
    open = false;
    if (refocus) {
      await tick();
      button?.focus();
    }
  }
  function onKey(e: KeyboardEvent) {
    if (open && e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      void close(true);
    }
  }
  function onPointer(e: PointerEvent) {
    if (open && root && !root.contains(e.target as Node)) void close(false);
  }

  const item =
    "flex w-full items-center gap-2.5 px-4 py-2 text-left text-sm text-black-800 transition-colors hover:bg-white-200 hover:text-black-900 dark:text-black-600 dark:hover:bg-navy-800 dark:hover:text-white-100";
  const seg = "rounded-md px-2 py-1 text-xs font-medium transition-colors";
</script>

<svelte:window onkeydowncapture={onKey} onpointerdown={onPointer} />

<div class="relative" bind:this={root}>
  <button
    type="button"
    bind:this={button}
    class="flex items-center gap-1 rounded-lg p-1 transition-colors hover:bg-white-300 focus:outline-none focus-visible:ring-2 focus-visible:ring-green-400 dark:hover:bg-navy-600"
    aria-label="Account menu"
    aria-haspopup="menu"
    aria-expanded={open}
    onclick={() => (open = !open)}
  >
    <span class="flex h-7 w-7 items-center justify-center rounded-full bg-green-200 text-xs font-semibold text-green-700 select-none">{initial}</span>
    <svg class="h-3.5 w-3.5 text-black-700 transition-transform dark:text-black-600 {open ? 'rotate-180' : ''}" fill="none" stroke="currentColor" stroke-width="2.5" viewBox="0 0 24 24" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="m6 9 6 6 6-6" /></svg>
  </button>
  {#if open}
    <div class="absolute right-0 top-full z-50 mt-2 w-52 rounded-xl border border-white-300 bg-white-100 shadow-lg dark:border-navy-600 dark:bg-navy-700" role="menu" aria-label="Account">
      <div class="border-b border-white-300 px-4 py-3 dark:border-navy-600">
        <p class="truncate text-sm font-medium text-black-900 dark:text-white-100">{viewerName || "You"}</p>
      </div>
      <div class="py-1">
        <button type="button" role="menuitem" class={item} onclick={() => { void close(false); onSettings(); }}>
          <svg class="h-4 w-4 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" viewBox="0 0 24 24" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.325.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 0 1 1.37.49l1.296 2.247a1.125 1.125 0 0 1-.26 1.431l-1.003.827c-.293.241-.438.613-.43.992a7.723 7.723 0 0 1 0 .255c-.008.378.137.75.43.991l1.004.827c.424.35.534.955.26 1.43l-1.298 2.247a1.125 1.125 0 0 1-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.47 6.47 0 0 1-.22.128c-.331.183-.581.495-.644.869l-.213 1.281c-.09.543-.56.94-1.11.94h-2.594c-.55 0-1.019-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 0 1-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 0 1-1.369-.49l-1.297-2.247a1.125 1.125 0 0 1 .26-1.431l1.004-.827c.292-.24.437-.613.43-.991a6.932 6.932 0 0 1 0-.255c.007-.38-.138-.751-.43-.992l-1.004-.827a1.125 1.125 0 0 1-.26-1.43l1.297-2.247a1.125 1.125 0 0 1 1.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.086.22-.128.332-.183.582-.495.644-.869l.214-1.28Z" /><path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z" /></svg>
          Team settings
        </button>
        {#if theme}
          <div class="flex items-center justify-between gap-2 px-4 py-2" role="group" aria-label="Theme">
            <span class="flex items-center gap-2.5 text-sm text-black-800 dark:text-black-600">
              <svg class="h-4 w-4 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" viewBox="0 0 24 24" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M21.752 15.002A9.72 9.72 0 0 1 18 15.75 9.75 9.75 0 0 1 8.25 6c0-1.33.266-2.597.748-3.752A9.753 9.753 0 0 0 3 11.25 9.75 9.75 0 0 0 12.75 21a9.753 9.753 0 0 0 9.002-5.998Z" /></svg>
              Theme
            </span>
            <span class="flex rounded-lg bg-white-300 p-0.5 dark:bg-navy-800">
              {#each [{ mode: "light", id: theme.light, label: "Light" }, { mode: "dark", id: theme.dark, label: "Dark" }] as t (t.mode)}
                <form method="POST" action="/theme" class="flex">
                  <input type="hidden" name="theme" value={t.id} />
                  <input type="hidden" name="redirect" value={here()} />
                  <button
                    type="submit"
                    role="menuitemradio"
                    aria-checked={theme.mode === t.mode}
                    class="{seg} {theme.mode === t.mode
                      ? 'bg-white-100 text-black-900 shadow-sm dark:bg-navy-600 dark:text-white-100'
                      : 'text-black-700 hover:text-black-900 dark:text-black-600 dark:hover:text-white-100'}"
                  >{t.label}</button>
                </form>
              {/each}
            </span>
          </div>
        {/if}
        <a href="/mini-tools" role="menuitem" class={item}>
          <svg class="h-4 w-4 shrink-0" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M6.5 2.5a3 3 0 00-3.5 4l-1 1 2.5 2.5 1-1a3 3 0 004-3.5L7.5 7 6 5.5l1-3z" stroke-linejoin="round"></path></svg>
          Mini Tools
        </a>
      </div>
      <div class="border-t border-white-300 py-1 dark:border-navy-600">
        <a href={exitHref} role="menuitem" class={item}>
          <svg class="h-4 w-4 shrink-0" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 4L2 8l4 4"></path><path d="M2 8h8a4 4 0 014 4v1"></path></svg>
          Switch to Agents
        </a>
      </div>
    </div>
  {/if}
</div>
