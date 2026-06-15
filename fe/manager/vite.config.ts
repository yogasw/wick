import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import path from "node:path";

/* fe/manager → up 2 → repo root. */
const REPO_ROOT = path.resolve(__dirname, "../..");
const OUT_DIR = path.resolve(REPO_ROOT, "internal/manager/dist/manager");

const WATCH = process.argv.includes("--watch") || process.argv.includes("-w");

export default defineConfig({
  plugins: [svelte()],
  base: "/modules/manager/app/",
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
    port: 5191,
    proxy: {
      "/manager": {
        target: process.env.WICK_PROXY ?? "http://localhost:9425",
        changeOrigin: true,
      },
      "/modules/manager": {
        target: process.env.WICK_PROXY ?? "http://localhost:9425",
        changeOrigin: true,
        bypass: (req) => {
          if (req.url?.startsWith("/modules/manager/app/")) {
            return req.url;
          }
        },
      },
      "/public": process.env.WICK_PROXY ?? "http://localhost:9425",
    },
  },
});
