# Trace rendering (`TraceBody`)

`TraceBody` renders one tool call input or tool result. The backend stamps a
`display` render hint on every trace event (`internal/agents/event/display.go`);
events recorded before that — and live events — are classified here by
`classify.ts`, a port with the same rules (parity test runs the Go fixture
`internal/agents/event/testdata/classify_cases.json`).

Every block gets the same frame: `ScrollBox` (max-height ~320px, Expand /
Collapse), a **Raw** toggle (the event text untouched; binaries show a short
preview, never broken base64), **Copy**, and a "Trace truncated" banner when
the store cut the payload.

Binary kinds (`image`, `pdf`, `audio`, `video`, `binary`) render as a chip
(`🖼 shot.png · PNG · 512 KB`). Nothing is decoded until the chip is clicked:
then the bytes come from `ctx.loadBlob(blob_ref)` (stored turn) or the
in-memory base64 in `display.data` (live turn).

## Adding a kind

1. Backend: `event.RegisterDetector(kind, priority, fn)` returning a `Display`
   with the new kind (and the TS twin `registerTraceDetector` in
   `classify.ts` if old traces should get it too — add a fixture row).
2. FE: write a component taking `{ display, ctx }` and register it:

   ```ts
   import { registerTraceRenderer } from "@wick-fe/common-ui";
   registerTraceRenderer("table", TableBlock);
   ```

No render site changes. A kind with no renderer falls back to `TextBlock`.

Syntax highlighting is app-provided: call `setTraceHighlighter(fn)` once.

## Skill cards

A read result whose path ends in `SKILL.md` (and a `Skill` tool result that
starts with frontmatter) is re-kinded `skill` in `TraceBody` via
`skillDisplay()` (`skill.ts`) and drawn by `SkillBlock`: name/description
from the frontmatter, scope from the path (Built-in wick / Global /
Project), the `<!-- MANAGED BY WICK … -->` comment shown as a badge, the
body as markdown, and a "partial" badge for offset/limit reads. FE-only:
Raw still shows the file exactly as the agent read it.
