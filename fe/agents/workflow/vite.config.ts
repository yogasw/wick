import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import path from "node:path";

// fe/agents/workflow → up 3 → repo root.
const REPO_ROOT = path.resolve(__dirname, "../../..");
const OUT_DIR = path.resolve(REPO_ROOT, "internal/tools/agents/dist/workflow");

export default defineConfig({
  plugins: [svelte()],
  base: "/tools/agents/agents-v2/workflow/",
  build: {
    outDir: OUT_DIR,
    emptyOutDir: true,
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
      // wicklab default port (cmd/lab). Override via $env:WICK_PORT
      // before `npm run dev:workflow` if your server runs elsewhere.
      "/tools/agents": process.env.WICK_PROXY ?? "http://localhost:9425",
      "/public": process.env.WICK_PROXY ?? "http://localhost:9425",
    },
  },
});
