import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import path from "node:path";

/* fe/manager → up 2 → repo root. */
const REPO_ROOT = path.resolve(__dirname, "../..");
const OUT_DIR = path.resolve(REPO_ROOT, "internal/manager/dist/manager");

const WATCH = process.argv.includes("--watch") || process.argv.includes("-w");

export default defineConfig({
  plugins: [svelte()],
  base: "/manager/_app/",
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
      /* Proxy the JSON + mutation API to the running wick server. The SPA
         page routes (/manager, /manager/connectors, …) and the Vite asset
         base (/manager/_app/) are served by Vite in dev, so only the API
         and OAuth surfaces forward upstream. */
      "/manager/api": process.env.WICK_PROXY ?? "http://localhost:9425",
      "/manager/connectors/custom": {
        target: process.env.WICK_PROXY ?? "http://localhost:9425",
        changeOrigin: true,
        /* POST/mutation + OAuth callbacks forward; the builder *page* GETs
           stay client-side so Vite serves the SPA shell. */
        bypass: (req) => {
          if (req.method === "GET" && !req.url?.includes("/oauth/")) {
            return req.url;
          }
        },
      },
      "/api/runs": process.env.WICK_PROXY ?? "http://localhost:9425",
      "/public": process.env.WICK_PROXY ?? "http://localhost:9425",
    },
  },
});
