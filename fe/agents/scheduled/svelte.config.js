import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";

// ───────────────────────────────────────────────────────────────────
// Svelte 5 gotchas the build will reject — keep this list near the
// compiler config so anyone touching templates sees it before they
// hit "error during build" three times in a row. Mirrored in
// fe/README.md under "Svelte 5 gotchas".
//
// 1. `{@const x = …}` is ONLY legal as the immediate child of a block
//    tag (`{#if}`, `{#each}`, `{:else}`, `{:then}`, `{:catch}`,
//    `{#snippet}`, `<svelte:fragment>`, `<svelte:boundary>`, or a
//    custom component). It does NOT work directly under HTML elements
//    like `<button>`, `<div>`, `<section>`. When the surrounding tag
//    is `<...>`, hoist the value into the `<script>` block as
//    `const x = $derived(...)` instead.
//
// 2. Class directives can't contain a `/` — `class:bg-emerald-500/25`
//    is a parser error. Use `class={cond ? 'bg-emerald-500/25' : ''}`
//    when you need a Tailwind opacity-modified class conditionally.
//
// 3. `{{ ... }}` in placeholders / attribute values is interpreted as
//    a Svelte expression. Wrap literal Go template strings in
//    `{"…{{.Field}}…"}` so the compiler treats them as plain text.
//
// 4. `<svelte:window>` (and other special elements) cannot live inside
//    `{#if}` — they must sit at the component's top level. Gate the
//    handler in JS instead of conditionally rendering the element.
// ───────────────────────────────────────────────────────────────────
export default {
  preprocess: vitePreprocess(),
  compilerOptions: {
    runes: true,
    // Inject component styles at runtime via <style> tags instead of
    // extracting to a separate stylesheet — the templ shell only loads
    // /public/css/app.css (Tailwind), so a Vite-emitted CSS file would
    // be unreachable. `injected` keeps every scoped style alive inside
    // the JS bundle so the dot-grid + node headers paint immediately.
    css: "injected",
  },
};
