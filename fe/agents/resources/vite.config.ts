import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import path from "node:path";

const REPO_ROOT = path.resolve(__dirname, "../../..");
const OUT_DIR = path.resolve(REPO_ROOT, "internal/tools/agents/dist/resources");

const WATCH = process.argv.includes("--watch") || process.argv.includes("-w");

export default defineConfig({
  plugins: [svelte()],
  // Assets are served by the agents SPA handler, which is mounted at
  // /workflow/ only (spa_handler.go spaPrefix). Every SPA lives under it —
  // overview, scm, and the rest — regardless of the page route the shell
  // is reached by. A base outside that prefix has no handler and 404s.
  base: "/tools/agents/workflow/resources/",
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
    port: 5187,
    proxy: {
      "/tools/agents": {
        target: process.env.WICK_PROXY ?? "http://localhost:9425",
        changeOrigin: true,
        bypass: (req) => {
          if (req.url?.startsWith("/tools/agents/workflow/resources/")) {
            return req.url;
          }
        },
      },
      "/public": process.env.WICK_PROXY ?? "http://localhost:9425",
    },
  },
});
