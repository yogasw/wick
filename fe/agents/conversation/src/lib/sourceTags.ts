import { esc } from "./markdown.js";
import { teammateName } from "./delegations.js";
import type { TeamTaskItem } from "./types/agents.js";

/* Per-point sources (grouped §3 summary answer): a Team agent ends each
   point it took from a teammate with [from:@handle] (several:
   [from:@a,@b]), and the web shows the teammate's name beside the point
   as a muted label. Only a tag at the end of a line (a bullet or a
   paragraph) counts; one mid-sentence is the agent talking about tags.

   A handle the turn did not delegate to is not a source of this answer:
   its tag is removed and nothing is shown, so a reply cannot claim a
   teammate it never asked. Tags inside code and math stay as written. */

const HANDLE = "@[A-Za-z0-9][A-Za-z0-9._-]*";
/* Matched only from the line's last "[from:" on, never scanned from
   every position, so a long run of spaces stays linear. */
const TAIL_RE = new RegExp(`^\\[from:[ \\t]*(${HANDLE}(?:[ \\t]*,[ \\t]*${HANDLE})*)[ \\t]*\\][ \\t]*$`);
/* The renderer's own block rules (common/md renderMarkdown): a fence is
   a line of exactly ``` plus an optional language, and a line of "$$"
   opens or closes display math. Mirrored so the pre-pass skips the same
   lines the renderer keeps verbatim. */
const FENCE_RE = /^\s*```\w*\s*$/;

/* Each tag becomes a private-use sentinel before markdown and is
   resolved after it, on the parsed DOM: only a text node outside code,
   pre, links and math gets the label; anywhere else (an attribute, a
   code block) the sentinel turns back into the tag as written. Markup is
   never spliced into the HTML string, so a label cannot land inside an
   href or a data-* attribute. */
const OPEN = "\uE000";
const CLOSE = "\uE001";
const SENTINEL_RE = /\uE000(\d+)\uE001/g;
const LITERAL_CTX = "code,pre,a,svg,script,style,textarea,[data-math],[data-math-src],[data-code]";

export const SOURCE_TAG_CLASS = "ml-1 font-sans text-[11px] text-black-700 dark:text-black-600";

export type SourceTag = {
  /** The tag as written, restored where no label may go. */
  raw: string;
  /** The teammates' names, "" when no handle is one of the turn's tasks. */
  names: string;
};

export type SourceTags = {
  /** The text with each end-of-line tag swapped for a sentinel; render
      it as markdown, then pass the HTML to apply(). */
  text: string;
  tags: SourceTag[];
  apply: (html: string) => string;
};

/** Swaps the source tags in an answer's text for sentinels that apply()
    turns into the muted name span once the text is rendered. Names come
    from the turn's own tasks, the way teammateName() names them. */
export function extractSourceTags(
  text: string,
  tasks: TeamTaskItem[],
  agents: Record<string, { name?: string }> = {},
): SourceTags {
  const tags: SourceTag[] = [];
  const apply = (html: string) => applySourceTags(html, tags);
  // A sentinel only ever comes from here, never from the text itself.
  let clean = (text || "").replace(/[\uE000\uE001]/g, "");
  if (!clean.includes("[from:")) return { text: clean, tags, apply };
  clean = clean.replace(/\r\n?/g, "\n");

  const byHandle = new Map<string, TeamTaskItem>();
  for (const t of tasks) if (t.to_handle) byHandle.set(t.to_handle.toLowerCase(), t);

  let inCode = false;
  let inMath = false;
  const out = clean.split("\n").map((line) => {
    if (FENCE_RE.test(line)) {
      inCode = !inCode;
      return line;
    }
    if (inCode) return line;
    if (line.trim() === "$$") {
      inMath = !inMath;
      return line;
    }
    if (inMath) return line;
    const at = line.lastIndexOf("[from:");
    if (at < 0) return line;
    const m = TAIL_RE.exec(line.slice(at));
    if (!m) return line;
    const head = line.slice(0, at);
    // Inside an open `code` span: left as written.
    if ((head.match(/`/g)?.length ?? 0) % 2 === 1) return line;
    const names: string[] = [];
    const seen = new Set<string>();
    for (const raw of m[1].split(",")) {
      const key = raw.trim().replace(/^@/, "").toLowerCase();
      const task = byHandle.get(key);
      if (!task || seen.has(key)) continue;
      seen.add(key);
      names.push(teammateName(task, agents));
    }
    tags.push({ raw: m[0].trimEnd(), names: names.join(", ") });
    /* The space keeps the sentinel apart from what precedes it: an
       autolinked URL would otherwise take it into its href. */
    return head.trimEnd() + " " + OPEN + (tags.length - 1) + CLOSE;
  });
  return { text: out.join("\n"), tags, apply };
}

/** renderMarkdown with the answer's source tags shown as name labels. */
export function renderWithSourceTags(
  text: string,
  tasks: TeamTaskItem[],
  agents: Record<string, { name?: string }> | undefined,
  render: (md: string) => string,
): string {
  const st = extractSourceTags(text, tasks, agents);
  return st.apply(render(st.text));
}

function restore(s: string, tags: SourceTag[]): string {
  return s.replace(SENTINEL_RE, (_, i) => tags[Number(i)]?.raw ?? "");
}

function applySourceTags(html: string, tags: SourceTag[]): string {
  if (!html.includes(OPEN)) return html;
  if (typeof document === "undefined") return restore(html, tags.map((t) => ({ ...t, raw: esc(t.raw) })));
  const tpl = document.createElement("template");
  tpl.innerHTML = html;
  for (const el of tpl.content.querySelectorAll("*")) {
    for (const a of [...el.attributes]) if (a.value.includes(OPEN)) el.setAttribute(a.name, restore(a.value, tags));
  }
  const walker = document.createTreeWalker(tpl.content, NodeFilter.SHOW_TEXT);
  const nodes: Text[] = [];
  for (let n = walker.nextNode(); n; n = walker.nextNode()) if ((n as Text).data.includes(OPEN)) nodes.push(n as Text);
  for (const node of nodes) {
    if (node.parentElement?.closest(LITERAL_CTX)) {
      node.data = restore(node.data, tags);
      continue;
    }
    const frag = document.createDocumentFragment();
    let last = 0;
    for (const m of node.data.matchAll(SENTINEL_RE)) {
      const tag = tags[Number(m[1])];
      // The space added before the sentinel: the label's ml-1 is the gap.
      frag.append(node.data.slice(last, m.index).replace(/[ \t]+$/, ""));
      if (tag?.names) {
        const span = document.createElement("span");
        span.className = SOURCE_TAG_CLASS;
        span.dataset.testid = "source-tag";
        span.textContent = tag.names;
        frag.append(span);
      }
      last = m.index! + m[0].length;
    }
    frag.append(node.data.slice(last));
    node.replaceWith(frag);
  }
  return tpl.innerHTML;
}
