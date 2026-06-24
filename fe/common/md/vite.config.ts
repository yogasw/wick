import { defineConfig } from "vite";
import path from "node:path";

/* fe/common/md → up 3 → repo root (fe/common/md → fe/common → fe → root). */
const REPO_ROOT = path.resolve(__dirname, "../../..");

/* Build the shared markdown renderer as a self-contained IIFE so ANY
   server-rendered page (templ + <script src>, no module loader) can use
   it via window.WickMarkdown — without porting the logic or adding a
   bundler to those pages. One bundle, one URL, loaded wherever needed.

   Output goes to web/public/lib/, which is already embedded
   (web.PublicFiles) and served globally at /public/ — so the file is
   reachable as /public/lib/wick-markdown.js from every page, not copied
   per module. Like the Vite SPA dist trees it is gitignored on master
   and built in CI by `(cd fe && npm run build)`; this package's `build`
   script auto-joins that run. */
export default defineConfig({
  build: {
    outDir: path.resolve(REPO_ROOT, "web/public/lib"),
    emptyOutDir: false, // shared static dir — don't wipe siblings
    lib: {
      entry: path.resolve(__dirname, "src/index.ts"),
      name: "WickMarkdown", // → window.WickMarkdown
      formats: ["iife"],
      fileName: () => "wick-markdown.js",
    },
    sourcemap: false,
  },
});
