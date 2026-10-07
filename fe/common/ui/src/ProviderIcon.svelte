<script lang="ts">
  import { providerBrand } from "./provider-brand.js";

  // value: provider type or "type/name". class: size/spacing utilities.
  let { value, class: cls = "w-4 h-4" }: { value: string; class?: string } = $props();
  const brand = $derived(providerBrand(value));
</script>

{#if brand === "codex"}
  <!-- OpenAI mark is monochrome: two static files toggled by the app's `.dark`
       class so it follows the in-app theme, not the OS-only prefers-color-scheme
       an <img> SVG would otherwise read. -->
  <img src="/public/img/providers/codex.svg" alt="" aria-hidden="true" draggable="false" data-testid="provider-icon" data-brand="codex" class={`${cls} object-contain dark:hidden`} />
  <img src="/public/img/providers/codex-dark.svg" alt="" aria-hidden="true" draggable="false" class={`${cls} object-contain hidden dark:block`} />
{:else if brand !== "other"}
  <!-- Multicolor brand marks served statically from /public/img/providers (embedded). -->
  <img src={`/public/img/providers/${brand}.svg`} alt="" aria-hidden="true" draggable="false" data-testid="provider-icon" data-brand={brand} class={`${cls} object-contain`} />
{:else}
  <svg class={cls} data-testid="provider-icon" data-brand="other" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="8" cy="5.5" r="2.5"/><path d="M3.5 13a4.5 4.5 0 019 0" stroke-linecap="round"/></svg>
{/if}
