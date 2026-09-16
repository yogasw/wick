import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import path from "node:path";

const REPO_ROOT = path.resolve(__dirname, "../../..");
const OUT_DIR = path.resolve(REPO_ROOT, "internal/admin/dist/analytics");

const WATCH = process.argv.includes("--watch") || process.argv.includes("-w");

export default defineConfig({
  plugins: [svelte()],
  // Served by the admin SPA asset handler, which is mounted at /admin/_app/.
  // A base outside that prefix has no handler and 404s.
  base: "/admin/_app/",
  build: {
    outDir: OUT_DIR,
    emptyOutDir: !WATCH,
    assetsDir: "assets",
    sourcemap: false,
  },
  resolve: {
    alias: { $lib: path.resolve(__dirname, "src/lib") },
  },
});
