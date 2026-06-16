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
