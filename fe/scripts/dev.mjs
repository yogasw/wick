#!/usr/bin/env node
// Single-watcher dev loop for the FE workspaces.
//
// The old version spawned one `vite build --watch` per workspace (~13
// long-lived Node processes, each holding a chokidar watcher + rollup +
// esbuild in memory even while idle — collectively pinning CPU/RAM). This
// keeps ONE process alive: a single recursive fs.watch over the source
// trees. On a change it debounces, maps the file to its owning
// workspace, and runs a one-shot `vite build` for just that workspace
// (spawned on demand, exits when done). Editing a common/* lib fans the
// rebuild out to every SPA that imports it.
//
// Result: idle cost is one Node process; a build only spins up while a
// file is actually changing.
import { spawn } from "node:child_process";
import { watch } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const FE_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const runner = process.versions.bun ? "bun" : "npm";

// dir (relative to fe/) → workspace name. Only these are built by the
// watcher; anything outside is ignored.
const WORKSPACES = {
  "agents/conversation": "@wick-fe/agents-conversation",
  "agents/new-session": "@wick-fe/agents-new-session",
  "agents/overview": "@wick-fe/agents-overview",
  "agents/presets": "@wick-fe/agents-presets",
  "agents/project-settings": "@wick-fe/agents-project-settings",
  "agents/providers": "@wick-fe/agents-providers",
  "agents/router9": "@wick-fe/agents-router9",
  "agents/scm": "@wick-fe/agents-scm",
  "agents/shell": "@wick-fe/agents-shell",
  "agents/skills": "@wick-fe/agents-skills",
  "agents/workflow": "@wick-fe/agents-workflow",
  "manager": "@wick-fe/manager",
  // common/md ships its own standalone bundle (web/public/lib/wick-markdown.js);
  // nothing imports it as a module, so it builds on its own here.
  "common/md": "@wick-fe/common-md",
};

// common/* lib dir → the workspaces that import it. Editing a lib rebuilds
// every consumer (common libs are bundled INTO each SPA, not shared at
// runtime, so a lib change is invisible until its consumers re-bundle).
const COMMON_CONSUMERS = {
  "common/api": [
    "agents/conversation", "agents/new-session", "agents/presets",
    "agents/project-settings", "agents/providers", "agents/scm",
    "agents/skills", "agents/workflow", "manager",
  ],
  "common/stores": [
    "agents/conversation", "agents/new-session", "agents/overview",
    "agents/presets", "agents/project-settings", "agents/providers",
    "agents/router9", "agents/scm", "agents/skills", "agents/workflow",
    "manager",
  ],
  "common/ui": [
    "agents/conversation", "agents/new-session", "agents/overview",
    "agents/presets", "agents/project-settings", "agents/providers",
    "agents/router9", "agents/scm", "agents/skills", "agents/workflow",
    "manager",
  ],
  "common/sse-worker": ["agents/conversation", "agents/scm"],
  "common/md": ["agents/conversation", "agents/skills"], // + its own bundle
};

// Dirs whose source we watch: each workspace + each common lib.
const WATCH_DIRS = [
  ...Object.keys(WORKSPACES),
  ...Object.keys(COMMON_CONSUMERS),
].filter((d, i, a) => a.indexOf(d) === i);

const IGNORE = /(^|[\\/])(node_modules|dist|\.svelte-kit|coverage)([\\/]|$)/;

function log(msg) {
  console.log(`[dev] ${msg}`);
}

const RULE = "=".repeat(60);

// A boxed banner. Blank lines + heavy rules on both sides so it stands
// out from vite's own build output (which can be dozens of lines).
function banner(msg) {
  console.log("");
  console.log(`[dev] ${RULE}`);
  console.log(`[dev]  ${msg}`);
  console.log(`[dev] ${RULE}`);
  console.log("");
}

// Resolve a changed file (absolute) to the set of workspace dirs to rebuild.
function dirsForChange(abs) {
  const rel = path.relative(FE_ROOT, abs).split(path.sep).join("/");
  if (IGNORE.test(rel)) return [];

  // Common lib change → fan out to consumers (+ the lib itself if it has
  // its own build entry in WORKSPACES, e.g. common/md).
  for (const [lib, consumers] of Object.entries(COMMON_CONSUMERS)) {
    if (rel === lib || rel.startsWith(lib + "/")) {
      const out = new Set(consumers);
      if (WORKSPACES[lib]) out.add(lib);
      return [...out];
    }
  }
  // Workspace-owned change → rebuild that workspace.
  for (const dir of Object.keys(WORKSPACES)) {
    if (rel === dir || rel.startsWith(dir + "/")) return [dir];
  }
  return [];
}

// Serialize builds: one vite process at a time, coalescing a queue of
// pending workspace dirs so a burst of saves produces a single rebuild.
let building = false;
const pending = new Set();

function enqueue(dirs) {
  for (const d of dirs) pending.add(d);
  drain();
}

function drain() {
  if (building || pending.size === 0) return;
  const dir = [...pending][0];
  pending.delete(dir);
  building = true;
  const ws = WORKSPACES[dir];
  const t0 = Date.now();
  banner(`rebuild ${ws}${pending.size ? ` (+${pending.size} queued)` : ""}`);
  buildOnce(ws).then((code) => {
    log(`${ws} ${code === 0 ? "✓ ok" : `✗ exit ${code}`} (${Date.now() - t0}ms)`);
    building = false;
    drain();
  });
}

// Debounce raw fs events (editors fire several per save) into one enqueue.
let timer = null;
const dirty = new Set();
function onChange(abs) {
  for (const d of dirsForChange(abs)) dirty.add(d);
  if (dirty.size === 0) return;
  clearTimeout(timer);
  timer = setTimeout(() => {
    const dirs = [...dirty];
    dirty.clear();
    enqueue(dirs);
  }, 150);
}

// Run one build attempt. Buffers output when quiet; resolves {code, out}.
//
// shell:true is REQUIRED on Windows — npm/bun are .cmd shims that Node 24
// refuses to spawn directly (EINVAL) without a shell. The DEP0190 warning
// this prints is harmless here: our args are a fixed, literal list (no
// user input to escape), so the "unescaped args" caveat doesn't apply.
function runBuild(ws, quiet) {
  return new Promise((resolve) => {
    const p = spawn(runner, ["--workspace", ws, "run", "build"], {
      cwd: FE_ROOT,
      stdio: quiet ? ["ignore", "pipe", "pipe"] : "inherit",
      shell: true,
    });
    let buf = "";
    if (quiet) {
      p.stdout.on("data", (d) => (buf += d));
      p.stderr.on("data", (d) => (buf += d));
    }
    p.on("exit", (code) => resolve({ code: code ?? 1, out: buf }));
  });
}

// Build one workspace once (non-watch). quiet=true buffers output so N
// parallel builds don't interleave logs; the buffer is flushed on final
// failure. Retries once on a transient EPERM/EBUSY — vite's emptyOutDir
// rmSync races a Windows file lock (the running Go server embeds/serves
// the same dist tree, or an AV/indexer holds the handle briefly).
async function buildOnce(ws, quiet = false) {
  let r = await runBuild(ws, quiet);
  if (r.code !== 0 && /EPERM|EBUSY|Permission denied/i.test(r.out)) {
    await new Promise((res) => setTimeout(res, 400));
    r = await runBuild(ws, quiet);
  }
  if (quiet && r.code !== 0 && r.out) process.stderr.write(r.out);
  return r.code;
}

// Concurrency for the INITIAL build only. A bounded pool so the first
// pass finishes fast without the all-at-once RAM spike that spawning 13
// vite builds simultaneously caused. Override with DEV_CONCURRENCY.
// Watching afterwards is always a single process (fs.watch), regardless.
const CONCURRENCY = Math.max(1, Number(process.env.DEV_CONCURRENCY) || 4);

// Initial pass: build every workspace once so the dist trees are fresh
// before we drop into watch mode. Runs CONCURRENCY builds at a time — a
// worker pool draining a shared index — so it's fast but RAM stays
// bounded. A percentage counter shows completed/total as each finishes.
async function initialBuild() {
  const targets = Object.values(WORKSPACES);
  const total = targets.length;
  const t0all = Date.now();
  let failed = 0;
  let done = 0;
  let next = 0;

  async function worker() {
    while (next < total) {
      const i = next++;
      const ws = targets[i];
      const t0 = Date.now();
      const code = await buildOnce(ws, true); // quiet: parallel logs would interleave
      done++;
      const pct = Math.round((done / total) * 100);
      if (code !== 0) {
        failed++;
        log(`[${done}/${total} ${String(pct).padStart(3)}%] ${ws} ✗ FAILED (exit ${code}) — continuing`);
      } else {
        log(`[${done}/${total} ${String(pct).padStart(3)}%] ${ws} ✓ ok (${Date.now() - t0}ms)`);
      }
    }
  }

  banner(`building ${total} modules · up to ${CONCURRENCY} at a time`);
  await Promise.all(Array.from({ length: Math.min(CONCURRENCY, total) }, worker));
  return { total, failed, ms: Date.now() - t0all };
}

banner(`runner=${runner} · initial build of ${Object.keys(WORKSPACES).length} modules, then watch`);
log("editing a common/* lib rebuilds every SPA that imports it");

const { total, failed, ms } = await initialBuild();

const watchers = [];
for (const dir of WATCH_DIRS) {
  const abs = path.join(FE_ROOT, dir, "src");
  try {
    const w = watch(abs, { recursive: true }, (_event, file) => {
      if (file) onChange(path.join(abs, file));
    });
    watchers.push(w);
  } catch (err) {
    log(`skip ${dir}/src (${err.code || err.message})`);
  }
}

if (failed) {
  banner(`⚠ all modules built (${total - failed}/${total} ok, ${failed} failed, ${(ms / 1000).toFixed(1)}s) · now watching ${watchers.length} src trees`);
} else {
  banner(`✓ all modules built (${total}/${total}, ${(ms / 1000).toFixed(1)}s) · now watching ${watchers.length} src trees — save a file to rebuild`);
}

process.on("SIGINT", () => {
  watchers.forEach((w) => w.close());
  process.exit(0);
});
