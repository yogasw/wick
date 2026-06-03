#!/usr/bin/env node
// Pre-build sanity check — scans .svelte files for the regex-detectable
// gotchas listed in svelte.config.js and fails fast with a friendlier
// hint than Svelte's generic parser error. Run automatically before
// `vite build` via the `prebuild` npm hook.
//
// Patterns covered:
//
//   • class:foo-bar/25={cond} — Tailwind opacity in a class directive
//     is a parser error (the `/` isn't a legal char in the directive
//     name). Suggestion: switch to `class={cond ? "..." : ""}`.
//
//   • placeholder="{{...}}" or value="{{...}}" — Svelte interprets
//     the inner braces as an expression. Suggestion: wrap in a JS
//     string: `placeholder={"{{.Event.Payload.id}}"}`.
//
// Patterns that need AST awareness ({@const} placement,
// <svelte:window> inside {#if}) stay enforced by the Svelte
// compiler — see the comment block in svelte.config.js for the full
// rule list.

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "src");

// Recursive .svelte file walk — no glob lib needed, plain fs is fine
// for the tens-of-files we have. Skips node_modules + build outputs.
function* walk(dir) {
  for (const entry of readdirSync(dir)) {
    if (entry === "node_modules" || entry === "dist" || entry.startsWith(".")) continue;
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      yield* walk(full);
    } else if (entry.endsWith(".svelte")) {
      yield full;
    }
  }
}

const checks = [
  {
    name: "class-directive-slash",
    // class:foo-bar/25={...} — slash invalid in a directive name.
    re: /class:[\w-]+\/\d+\s*=\s*\{/g,
    hint: 'class directives can\'t contain "/". Use `class={cond ? "bg-emerald-500/25" : ""}` instead.',
  },
  {
    name: "go-template-in-attr",
    // placeholder="{{...}}" or value="{{...}}" — double-brace inside
    // a quoted attribute is parsed as a Svelte expression. Wrap the
    // string in {"..."} braces so Svelte treats it as plain text.
    re: /\b(placeholder|value|title)="\{\{[^"]+\}\}"/g,
    hint: 'Go template literal in an attribute needs JS-string wrapping: `placeholder={"{{.Field}}"}` not `placeholder="{{.Field}}"`.',
  },
];

let failed = 0;
for (const file of walk(root)) {
  const src = readFileSync(file, "utf8");
  for (const check of checks) {
    check.re.lastIndex = 0;
    let m;
    while ((m = check.re.exec(src)) !== null) {
      // Compute the 1-indexed line so the message reads like a normal
      // compiler error.
      const line = src.slice(0, m.index).split("\n").length;
      const rel = relative(join(here, ".."), file).replace(/\\/g, "/");
      console.error(
        `\x1b[31m✗\x1b[0m svelte-gotcha[${check.name}] ${rel}:${line}\n` +
          `  ${m[0]}\n` +
          `  ${check.hint}\n` +
          `  See gotcha list at the top of svelte.config.js`,
      );
      failed++;
    }
  }
}

if (failed > 0) {
  console.error(
    `\n\x1b[31m${failed} Svelte gotcha violation${failed === 1 ? "" : "s"} blocked the build.\x1b[0m`,
  );
  process.exit(1);
}
