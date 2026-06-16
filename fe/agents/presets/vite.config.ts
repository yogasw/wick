import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import path from "node:path";

const REPO_ROOT = path.resolve(__dirname, "../../..");
const OUT_DIR = path.resolve(REPO_ROOT, "internal/tools/agents/dist/presets");

const WATCH = process.argv.includes("--watch") || process.argv.includes("-w");

export default defineConfig({
  plugins: [svelte()],
  base: "/tools/agents/workflow/presets/",
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
    port: 5179,
    proxy: {
      "/tools/agents": {
        target: process.env.WICK_PROXY ?? "http://localhost:9425",
        changeOrigin: true,
        bypass: (req) => {
          if (req.url?.startsWith("/tools/agents/workflow/presets/")) {
            return req.url;
          }
        },
      },
      "/public": process.env.WICK_PROXY ?? "http://localhost:9425",
    },
  },
});
