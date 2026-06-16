import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import path from "node:path";

// fe/agents/skills → up 3 → repo root.
const REPO_ROOT = path.resolve(__dirname, "../../..");
const OUT_DIR = path.resolve(REPO_ROOT, "internal/tools/agents/dist/skills");

// See workflow/vite.config.ts — watch mode skips the outDir wipe to
// avoid Windows EPERM when wick holds the previous index.html open.
const WATCH = process.argv.includes("--watch") || process.argv.includes("-w");

export default defineConfig({
  plugins: [svelte()],
  base: "/tools/agents/workflow/skills/",
  build: {
    outDir: OUT_DIR,
    emptyOutDir: !WATCH,
    assetsDir: "assets",
    sourcemap: false,
  },
  resolve: {
    alias: {
      $lib: path.resolve(__dirname, "src/lib"),
    },
  },
  server: {
    port: 5176,
    proxy: {
      "/tools/agents": {
        target: process.env.WICK_PROXY ?? "http://localhost:9425",
        changeOrigin: true,
        bypass: (req) => {
          if (req.url?.startsWith("/tools/agents/workflow/skills/")) {
            return req.url;
          }
        },
      },
      "/public": process.env.WICK_PROXY ?? "http://localhost:9425",
    },
  },
});
