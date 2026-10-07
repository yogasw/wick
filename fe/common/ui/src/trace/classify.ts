/* classify.ts — the FE port of event.ClassifyCall / ClassifyResult
   (internal/agents/event/display*.go), for trace events recorded before the
   backend stamped a Display on them, and for live events that carry none.

   Same rules, same priorities, same fixture: __tests__/traceClassify.test.ts
   runs internal/agents/event/testdata/classify_cases.json, so a rule changed
   on one side and not the other fails a test. One deliberate difference:
   binaries are NEVER decoded here — mime comes from the declared type or
   the first 32 base64 chars, size from the base64 length, and the payload
   rides along in `data` until a chip is clicked. */
import { humanBytes, utf8Bytes } from "./format.js";
import { isBinaryKind, type TraceDisplay } from "./types.js";

export type DetectInput = {
  toolName: string;
  tool: string;
  text: string;
  call: boolean;
  isError: boolean;
  callInfo?: TraceDisplay | null;
};
export type TraceDetector = (in_: DetectInput) => TraceDisplay | null;

const detectors: { priority: number; seq: number; fn: TraceDetector }[] = [];

/* Mirror of event.RegisterDetector: higher priority first, ties keep
   registration order, first non-null wins. */
export function registerTraceDetector(priority: number, fn: TraceDetector): void {
  detectors.push({ priority, seq: detectors.length, fn });
  detectors.sort((a, b) => b.priority - a.priority || a.seq - b.seq);
}

function runDetectors(in_: DetectInput): TraceDisplay {
  for (const d of detectors) {
    const out = d.fn(in_);
    if (out) return out;
  }
  return { kind: "text", body: in_.text };
}

export function classifyResult(toolName: string, raw: string, isError = false, call?: TraceDisplay | null): TraceDisplay {
  const [text, bins] = unwrapPayload(raw);
  const in_: DetectInput = { toolName, tool: toolFamily(toolName), text, call: false, isError, callInfo: call };
  let d: TraceDisplay;
  if (bins.length > 0 && text.trim() === "") {
    d = { ...bins[0] };
    if (bins.length > 1) d.parts = bins.slice(1);
  } else {
    d = runDetectors(in_);
    if (bins.length) d.parts = [...(d.parts ?? []), ...bins];
  }
  finish(d, in_, raw);
  if (isBinaryKind(d.kind) && call?.path) d.name = call.path.replace(/\\/g, "/").split("/").pop();
  return d;
}

export function classifyCall(toolName: string, input: string): TraceDisplay {
  const in_: DetectInput = { toolName, tool: toolFamily(toolName), text: input, call: true, isError: false };
  const d = runDetectors(in_);
  finish(d, in_, input);
  return d;
}

function finish(d: TraceDisplay, in_: DetectInput, raw: string): void {
  if (!d.tool && in_.tool) d.tool = in_.tool;
  if (!d.title && in_.toolName) d.title = in_.toolName;
  if (!d.original_bytes) d.original_bytes = isBinaryKind(d.kind) ? 0 : utf8Bytes(raw);
}

export function toolFamily(name: string): string {
  let n = (name ?? "").trim().toLowerCase();
  if (n.startsWith("functions.")) n = n.slice("functions.".length);
  if (n.startsWith("mcp__")) return "mcp";
  switch (n) {
    case "bash": case "shell": case "exec_command": case "local_shell": case "run_command": case "run_shell_command":
    case "execute_command": case "terminal": case "exec": case "container.exec": case "write_stdin":
      return "bash";
    case "read": case "read_file": case "view": case "cat": case "readfile":
      return "read";
    case "write": case "write_file": case "create_file": case "writefile":
      return "write";
    case "edit": case "multiedit": case "multi_edit": case "str_replace": case "str_replace_editor": case "edit_file": case "replace": case "notebookedit":
      return "edit";
    case "apply_patch": case "patch":
      return "patch";
    case "grep": case "rg": case "ripgrep": case "search": case "grep_search": case "search_files": case "codesearch":
      return "grep";
    case "glob": case "find": case "find_files": case "list_files": case "ls": case "list": case "list_dir":
      return "glob";
    case "websearch": case "web_search": case "web_search_preview":
      return "web_search";
    case "webfetch": case "web_fetch": case "fetch":
      return "fetch";
    case "wick_execute":
      return "mcp";
  }
  return n.includes(".") ? "mcp" : "";
}

// ── unwrap ──────────────────────────────────────────────────────────────

type ContentBlock = {
  type?: string;
  text?: string;
  data?: string;
  mimeType?: string;
  source?: { type?: string; media_type?: string; data?: string };
};

const BLOCK_TYPES = new Set(["text", "image", "document", "audio", "video", "resource", "resource_link", "file"]);
const isBlock = (b: unknown): b is ContentBlock =>
  !!b && typeof b === "object" && !Array.isArray(b) && BLOCK_TYPES.has(String((b as ContentBlock).type));

export function unwrapPayload(raw: string): [string, TraceDisplay[]] {
  const t = raw.trim();
  if (t.length >= 2 && t[0] === '"' && t[t.length - 1] === '"') {
    try {
      const s = JSON.parse(t);
      if (typeof s === "string") return unwrapPayload(s);
    } catch { /* not a string literal */ }
  }
  if (t.length > 1 && (t[0] === "[" || t[0] === "{") && t.includes('"type"')) {
    let blocks: unknown[] = [];
    try {
      const v = JSON.parse(t);
      blocks = Array.isArray(v) ? v : [v];
    } catch { /* not JSON */ }
    if (blocks.length > 0 && blocks.every(isBlock)) {
      const texts: string[] = [];
      const bins: TraceDisplay[] = [];
      for (const b of blocks as ContentBlock[]) {
        if (b.type === "text") {
          texts.push(b.text ?? "");
          continue;
        }
        let data = b.data ?? "", mime = b.mimeType ?? "";
        if (b.source && b.source.type === "base64") {
          data = b.source.data ?? "";
          mime = b.source.media_type ?? "";
        }
        const d = data ? binaryDisplay(data, mime) : null;
        if (d) bins.push(d);
      }
      return [texts.join("\n"), bins];
    }
  }
  return [raw, []];
}

// ── binary (no decode) ──────────────────────────────────────────────────

const DATA_URL_RE = /^data:([a-zA-Z0-9.+/-]+)?(;[^,]*)?;base64,/;
const B64_RE = /^[A-Za-z0-9+/=\s]*$/;

function decodeHead(b64: string): Uint8Array | null {
  let head = b64.replace(/\s+/g, "").slice(0, 32);
  head = head.slice(0, head.length - (head.length % 4));
  try {
    const s = atob(head);
    return Uint8Array.from(s, (c) => c.charCodeAt(0));
  } catch {
    return null;
  }
}

function b64Bytes(b64: string): number {
  const clean = b64.replace(/\s+/g, "");
  const pad = clean.endsWith("==") ? 2 : clean.endsWith("=") ? 1 : 0;
  return Math.floor((clean.length * 3) / 4) - pad;
}

export function sniffMime(b: Uint8Array): string {
  const s = (i: number, j: number) => String.fromCharCode(...b.slice(i, j));
  if (b.length >= 8 && s(0, 8) === "\x89PNG\r\n\x1a\n") return "image/png";
  if (b.length >= 3 && b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff) return "image/jpeg";
  if (b.length >= 6 && (s(0, 6) === "GIF87a" || s(0, 6) === "GIF89a")) return "image/gif";
  if (b.length >= 12 && s(0, 4) === "RIFF" && s(8, 12) === "WEBP") return "image/webp";
  if (b.length >= 12 && s(0, 4) === "RIFF" && s(8, 12) === "WAVE") return "audio/wav";
  if (b.length >= 5 && s(0, 5) === "%PDF-") return "application/pdf";
  if (b.length >= 4 && s(0, 4) === "OggS") return "audio/ogg";
  if (b.length >= 3 && s(0, 3) === "ID3") return "audio/mpeg";
  if (b.length >= 12 && s(4, 8) === "ftyp") return "video/mp4";
  if (b.length >= 4 && b[0] === 0x1a && b[1] === 0x45 && b[2] === 0xdf && b[3] === 0xa3) return "video/webm";
  return "application/octet-stream";
}

function binaryKind(mime: string): string {
  if (mime.startsWith("image/")) return "image";
  if (mime === "application/pdf") return "pdf";
  if (mime.startsWith("audio/")) return "audio";
  if (mime.startsWith("video/")) return "video";
  return "binary";
}

export function mimeLabel(mime: string): string {
  let sub = mime.includes("/") ? mime.slice(mime.indexOf("/") + 1) : mime;
  sub = sub.replace(/^x-/, "").split("+")[0];
  return !sub || sub === "octet-stream" ? "BINARY" : sub.toUpperCase();
}

function defaultName(kind: string, mime: string): string {
  let ext = mimeLabel(mime).toLowerCase();
  if (ext === "jpeg") ext = "jpg";
  if (ext === "binary") ext = "bin";
  return `${kind === "pdf" || kind === "binary" ? "file" : kind}.${ext}`;
}

function binaryDisplay(b64: string, mime: string): TraceDisplay | null {
  if (!B64_RE.test(b64)) return null;
  if (!mime) {
    const head = decodeHead(b64);
    if (!head) return null;
    mime = sniffMime(head);
  }
  const kind = binaryKind(mime);
  const n = b64Bytes(b64);
  return { kind, mime, original_bytes: n, name: defaultName(kind, mime), summary: `${mimeLabel(mime)} · ${humanBytes(n)}`, data: b64 };
}

function looksBase64(s: string): boolean {
  return s.length >= 64 && /^[A-Za-z0-9+/=\n\r]*$/.test(s);
}

function binaryMagic(s: string): boolean {
  const head = decodeHead(s);
  return !!head && sniffMime(head) !== "application/octet-stream";
}

// ── helpers ─────────────────────────────────────────────────────────────

type Obj = Record<string, unknown>;

function jsonObject(s: string): Obj | null {
  const t = s.trim();
  if (t.length < 2 || t[0] !== "{") return null;
  try {
    const v = JSON.parse(t);
    return v && typeof v === "object" && !Array.isArray(v) ? (v as Obj) : null;
  } catch {
    return null;
  }
}

function str(m: Obj, ...keys: string[]): string {
  for (const k of keys) {
    const v = m[k];
    if (typeof v === "string" && v !== "") return v;
  }
  return "";
}

function firstLine(s: string, max: number): string {
  s = s.trim();
  const i = s.indexOf("\n");
  if (i >= 0) s = s.slice(0, i) + " …";
  const r = Array.from(s);
  return r.length > max ? r.slice(0, max).join("") + "…" : s;
}

const EXT_LANG: Record<string, string> = {
  ".go": "go", ".ts": "typescript", ".tsx": "tsx", ".js": "javascript", ".jsx": "jsx",
  ".mjs": "javascript", ".py": "python", ".rb": "ruby", ".rs": "rust", ".java": "java",
  ".kt": "kotlin", ".swift": "swift", ".c": "c", ".h": "c", ".cpp": "cpp", ".cc": "cpp",
  ".cs": "csharp", ".php": "php", ".ex": "elixir", ".exs": "elixir", ".sh": "shell",
  ".bash": "shell", ".zsh": "shell", ".json": "json", ".yaml": "yaml", ".yml": "yaml",
  ".toml": "toml", ".md": "markdown", ".sql": "sql", ".html": "html", ".css": "css",
  ".scss": "scss", ".svelte": "svelte", ".vue": "vue", ".templ": "templ", ".xml": "xml",
  ".svg": "xml", ".proto": "protobuf", ".lua": "lua", ".tf": "hcl", ".ini": "ini",
};

export function langForPath(p: string): string {
  const base = (p.replace(/\\/g, "/").split("/").pop() ?? "").toLowerCase();
  if (base === "dockerfile") return "dockerfile";
  if (base === "makefile") return "makefile";
  const dot = base.lastIndexOf(".");
  return dot >= 0 ? (EXT_LANG[base.slice(dot)] ?? "") : "";
}

function isJSON(s: string): boolean {
  const t = s.trim();
  if (t.length < 2 || (t[0] !== "{" && t[0] !== "[")) return false;
  try {
    JSON.parse(t);
    return true;
  } catch {
    return false;
  }
}

const nonEmpty = (...ss: string[]) => ss.filter((s) => s !== "");

// ── built-in detectors (priorities match display_detectors.go) ─────────

registerTraceDetector(100, (in_) => {
  const t = in_.text.trim();
  const m = DATA_URL_RE.exec(t);
  if (m) return binaryDisplay(t.slice(m[0].length), m[1] ?? "");
  if (in_.call || !looksBase64(t) || !binaryMagic(t)) return null;
  return binaryDisplay(t, "");
});

const ERROR_PREFIX_RE = /^(<tool_use_error>|error:|fatal:|exception:)/i;

registerTraceDetector(90, (in_) => {
  if (in_.call) return null;
  let t = in_.text.trim();
  // A failed shell command is still terminal output (exit badge, not banner).
  if (in_.tool === "bash" && t !== "") return null;
  if (!in_.isError && !ERROR_PREFIX_RE.test(t)) return null;
  t = t.replace(/^<tool_use_error>/, "").replace(/<\/tool_use_error>$/, "");
  return { kind: "error", body: t.trim(), summary: firstLine(t, 120) };
});

const SHELL_C_RE = /^(?:\/usr)?(?:\/bin\/)?(?:ba|z)?sh -l?c (['"])([\s\S]*)['"]$/;

function unwrapShellC(cmd: string): string {
  const m = SHELL_C_RE.exec(cmd.trim());
  if (!m || m[2].includes(m[1])) return cmd;
  return m[2];
}

function shellJoin(argv: unknown[]): string {
  const parts = argv.filter((a): a is string => typeof a === "string");
  if (parts.length === 3 && (parts[1] === "-lc" || parts[1] === "-c") && parts[0].endsWith("sh")) return parts[2];
  return parts.join(" ");
}

registerTraceDetector(80, (in_) => {
  if (!in_.call || in_.tool !== "bash") return null;
  const d: TraceDisplay = { kind: "command", lang: "shell" };
  const m = jsonObject(in_.text);
  if (!m) {
    d.command = in_.text.trim();
  } else {
    d.command = str(m, "command", "cmd", "chars");
    if (Array.isArray(m.command)) d.command = shellJoin(m.command);
    d.cwd = str(m, "cwd", "workdir", "working_directory", "dir") || undefined;
    d.summary = str(m, "description", "justification") || undefined;
    if (typeof m.timeout === "number") d.timeout = Math.trunc(m.timeout);
    else if (typeof m.timeout_ms === "number") d.timeout = Math.trunc(m.timeout_ms);
  }
  d.command = unwrapShellC(d.command ?? "");
  if (!d.summary) d.summary = firstLine(d.command, 120);
  d.body = d.command;
  return d;
});

const PATCH_FILE_RE = /^\*\*\* (?:Update|Add|Delete) File: (.+)$|^\+\+\+ (?:b\/)?(.+)$/m;

function patchPath(patch: string): string {
  const m = PATCH_FILE_RE.exec(patch);
  if (!m) return "";
  return (m[1] ?? m[2] ?? "").trim();
}

function splitLines(s: string): string[] {
  return s === "" ? [] : s.replace(/\n$/, "").split("\n");
}

function editDiff(p: string, m: Obj): string {
  const pairs: [string, string][] = [];
  if (Array.isArray(m.edits)) {
    for (const e of m.edits) {
      if (e && typeof e === "object") {
        const em = e as Obj;
        pairs.push([str(em, "old_string", "oldString", "oldText"), str(em, "new_string", "newString", "newText")]);
      }
    }
  } else {
    pairs.push([str(m, "old_string", "oldString", "old_str", "oldText"), str(m, "new_string", "newString", "new_str", "newText")]);
  }
  let out = `--- ${p}\n+++ ${p}\n`;
  for (const [o, n] of pairs) {
    out += "@@\n";
    for (const l of splitLines(o)) out += `-${l}\n`;
    for (const l of splitLines(n)) out += `+${l}\n`;
  }
  return out;
}

registerTraceDetector(80, (in_) => {
  if (!in_.call || !["read", "write", "edit", "patch"].includes(in_.tool)) return null;
  const m = jsonObject(in_.text);
  if (in_.tool === "patch") {
    const patch = m ? str(m, "input", "patch", "content") : in_.text;
    const p = patchPath(patch);
    return { kind: "diff", lang: "diff", body: patch, path: p || undefined, summary: p || undefined };
  }
  if (!m) return null;
  const p = str(m, "file_path", "path", "filePath", "filename", "notebook_path");
  const d: TraceDisplay = { kind: "file", path: p, lang: langForPath(p), summary: p };
  if (in_.tool === "write") d.body = str(m, "content", "contents", "text");
  else if (in_.tool === "edit") {
    d.kind = "diff";
    d.lang = "diff";
    d.body = editDiff(p, m);
  } else if (typeof m.offset === "number") d.summary += ` @${Math.trunc(m.offset)}`;
  return d;
});

registerTraceDetector(80, (in_) => {
  if (!in_.call || !["grep", "glob", "web_search"].includes(in_.tool)) return null;
  const d: TraceDisplay = { kind: "search" };
  let extra = "";
  const m = jsonObject(in_.text);
  if (m) {
    d.pattern = str(m, "pattern", "query", "regex", "q");
    d.path = str(m, "path", "dir", "directory", "cwd") || undefined;
    const g = str(m, "glob", "include");
    if (g && d.pattern !== g) extra = g;
  } else {
    d.pattern = in_.text.trim();
  }
  d.summary = nonEmpty(d.pattern ?? "", extra, d.path ?? "").join(" · ").trim();
  d.body = d.pattern;
  return d;
});

registerTraceDetector(80, (in_) => {
  if (!in_.call || in_.tool !== "mcp") return null;
  const d: TraceDisplay = { kind: "mcp", lang: "json", body: in_.text };
  const name = in_.toolName;
  let connector = "", op = "";
  if (name.startsWith("mcp__")) {
    const rest = name.slice(5);
    const i = rest.indexOf("__");
    if (i >= 0) [connector, op] = [rest.slice(0, i), rest.slice(i + 2)];
    else op = rest;
  } else if (name.includes(".")) {
    const i = name.lastIndexOf(".");
    [connector, op] = [name.slice(0, i), name.slice(i + 1)];
  } else op = name;
  if (op.endsWith("wick_execute")) {
    const m = jsonObject(in_.text);
    if (m) {
      let id = str(m, "tool_id");
      if (id) {
        id = id.replace(/^conn:/, "");
        const i = id.indexOf("/");
        if (i >= 0) [connector, op] = [id.slice(0, i), id.slice(i + 1)];
        else op = id;
      }
      if ("params" in m) d.body = JSON.stringify(m.params, null, 2);
    }
  }
  if (connector) d.connector = connector;
  if (op) d.op = op;
  d.summary = nonEmpty(connector, op).join(" · ");
  return d;
});

registerTraceDetector(70, (in_) => {
  if (in_.call) return null;
  switch (in_.tool) {
    case "bash":
      return { kind: "terminal", body: in_.text };
    case "read": {
      const d: TraceDisplay = { kind: "code", body: in_.text };
      if (in_.callInfo) {
        d.path = in_.callInfo.path;
        d.lang = in_.callInfo.lang;
      }
      if (d.lang === "markdown") d.kind = "markdown";
      return d;
    }
    case "grep":
    case "glob":
      if (in_.text.trim() === "" || isJSON(in_.text)) return null;
      return { kind: "search", body: in_.text };
  }
  return null;
});

registerTraceDetector(50, (in_) => (isJSON(in_.text) ? { kind: "json", lang: "json", body: in_.text } : null));

const DIFF_HUNK_RE = /^@@ .* @@/m;
const DIFF_HDR_RE = /^(diff --git |--- \S|\+\+\+ \S|\*\*\* Begin Patch)/m;

registerTraceDetector(40, (in_) => {
  const t = in_.text;
  if (!DIFF_HDR_RE.test(t) || !(DIFF_HUNK_RE.test(t) || t.includes("*** Begin Patch"))) return null;
  const p = patchPath(t);
  return { kind: "diff", lang: "diff", body: t, path: p || undefined };
});

const MD_HEADING_RE = /^#{1,6} \S/m;
const MD_FENCE_RE = /^```/m;
const MD_TABLE_RE = /^\|.*\|\s*\n\|\s*:?-{3,}/m;
const MD_LIST_RE = /^\s*(?:[-*+]|\d+\.) \S/gm;
const MD_INLINE_RE = /\*\*[^*\n]+\*\*|\[[^\]\n]+\]\([^)\s]+\)/;

registerTraceDetector(30, (in_) => {
  const t = in_.text;
  const lists = (t.match(MD_LIST_RE) ?? []).length;
  if (MD_HEADING_RE.test(t) || MD_FENCE_RE.test(t) || MD_TABLE_RE.test(t) || (lists >= 2 && MD_INLINE_RE.test(t))) {
    return { kind: "markdown", lang: "markdown", body: t };
  }
  return null;
});
