import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import path from "node:path";

// fe/agents/workflow → up 3 → repo root.
const REPO_ROOT = path.resolve(__dirname, "../../..");
const OUT_DIR = path.resolve(REPO_ROOT, "internal/tools/agents/dist/workflow");

// Watch mode (`vite build --watch`) and one-shot build behave
// differently when the destination is being read by another process.
// In watch mode wick may hold the previous index.html open while Vite
// tries to nuke the directory → EPERM on Windows. Skip the empty step
// in that case; stale asset bundles accumulate but each ships with a
// unique hash so they never clash with the live one. A non-watch
// build keeps the original "wipe outDir first" behaviour.
const WATCH = process.argv.includes("--watch") || process.argv.includes("-w");

export default defineConfig({
  plugins: [svelte()],
  base: "/tools/agents/workflow/workflow/",
  build: {
    outDir: OUT_DIR,
    emptyOutDir: !WATCH,
    assetsDir: "assets",
    sourcemap: true,
  },
  resolve: {
    alias: {
      $lib: path.resolve(__dirname, "src/lib"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      // wicklab default port (cmd/lab). Override via $env:WICK_PROXY
      // before `npm run dev:workflow` if your server runs elsewhere.
      //
      // `bypass` lets Vite handle the SPA's own base path locally so
      // HMR works without round-tripping the shell through wick. Only
      // API/templ endpoints (under /tools/agents but outside the SPA
      // base) forward upstream — the SPA root is served by Vite even
      // when wick is offline.
      "/tools/agents": {
        target: process.env.WICK_PROXY ?? "http://localhost:9425",
        changeOrigin: true,
        bypass: (req) => {
          if (req.url?.startsWith("/tools/agents/workflow/workflow/")) {
            return req.url;
          }
        },
      },
      "/public": process.env.WICK_PROXY ?? "http://localhost:9425",
    },
  },
});
