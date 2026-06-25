#!/usr/bin/env node
// Spawns all workspace build:watch processes in parallel.
// Supports both bun and npm — prefers bun when available.
import { spawn } from "node:child_process";

// Detect package manager: bun if invoked via bun, else fall back to npm.
const runner = process.versions.bun ? "bun" : "npm";
console.log(`[dev] runner=${runner}`);

const workspaces = [
  "@wick-fe/agents-conversation",
  "@wick-fe/agents-new-session",
  "@wick-fe/agents-overview",
  "@wick-fe/agents-presets",
  "@wick-fe/agents-project-settings",
  "@wick-fe/agents-providers",
  "@wick-fe/agents-scm",
  "@wick-fe/agents-shell",
  "@wick-fe/agents-skills",
  "@wick-fe/agents-workflow",
  "@wick-fe/manager",
  // common/md is the one common/* lib that ships its OWN standalone bundle
  // (web/public/lib/wick-markdown.js, served as /public/lib/wick-markdown.js)
  // instead of being imported into an agents SPA. Nothing else builds it, so
  // without it here `npm run dev` leaves the file missing and pages that load
  // the markdown renderer (e.g. the Software Update "What's new" changelog)
  // 404 on the script and fall back to raw, unrendered markdown.
  "@wick-fe/common-md",
];

const procs = workspaces.map((ws) => {
  const p = spawn(
    runner,
    ["--workspace", ws, "run", "build:watch"],
    { stdio: "inherit", shell: true }
  );
  p.on("exit", (code) => {
    if (code !== 0) process.exit(code ?? 1);
  });
  return p;
});

process.on("SIGINT", () => procs.forEach((p) => p.kill("SIGINT")));
