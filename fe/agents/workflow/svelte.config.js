import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";

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
