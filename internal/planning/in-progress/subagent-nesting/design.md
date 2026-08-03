# Sub-agent sessions: nested on disk, inspectable in a modal

Sub-agent sessions today are real sessions that live flat in
`sessions/sub-<uuid>/`, next to every human conversation, and the rail panel
that lists them can only highlight a row — clicking one shows nothing. This
change files a child's folder *inside* its parent's, and turns the rail row
into a modal that renders the child's full transcript (thinking, tool calls,
results) with a composer to keep asking it questions after it has finished.

## TODO

- [x] `config.Layout`: nested `SessionDir` path math + `SubSessionID` / `ParentOfSubSession` helpers
- [x] `delegation/run.go`: derive child ID with the new scheme
- [x] `session.ListAll` / `ListChildren`, wired into the boot registry and the workspace sweeper
- [x] Registry: publish a child on create/start, drop the subtree on delete
- [x] Go tests: layout path math, nested create/load/delete, cascade, child-id derivation
- [x] FE: `SubAgentModal.svelte` — breadcrumb, transcript, composer, Stop
- [x] FE: wire `DetailView` rail click → open modal
- [x] FE tests: modal renders transcript, breadcrumb push/pop, composer send, Stop gating
- [x] `graphify update .`

## What exists today

- A delegation spawns a **real session** for the child
  (`delegation.PoolRunner.EnsureChildSession`) whose ID is `"sub-" + delegationID`.
- `session.Meta.ParentSessionID` already marks a session as a child, and that
  is what hides it from the conversation list.
- `SubAgentPanel.svelte` lists children with status, turns, result preview and
  a working **Stop** button. `onSelect` only sets `selectedSubAgent`, which is
  used for nothing but the row's border colour.
- `TakeOver` (`delegation/takeover.go`) lets a human message a child, but only
  while `status == running` and only if the role opted in with `AllowTakeOver`.

## Decisions

1. **Nested per immediate parent**, not flat and not root-flattened. A child's
   folder lives at `<parent dir>/subagents/<seg>/`, recursively.
2. **Modal**, not an in-place replace. Esc closes; an overlay makes it obvious
   you are no longer looking at the main conversation.
3. **Resume is local.** Chatting with a finished child does not reopen its
   delegation and does not send anything back to the leader.

## Disk layout

```
sessions/
  a1b2c3d4-…-root/                 ← human conversation
    conversation.jsonl
    thinking/
    subagents/
      9f2c81ab40de/                ← child (test-agent)
        conversation.jsonl
        thinking/
        subagents/
          71ee03cc95a1/            ← grandchild, depth 2
            conversation.jsonl
```

### ID ↔ path

The session ID encodes the whole chain, so `Layout.SessionDir` stays a **pure
function** of the ID — no DB lookup, no index file, no boot walk, nothing that
can fall out of sync with the tree:

```
sep       = "--sub-"
child ID  = <parentID> + sep + <12 hex>
dir       = SessionsDir()/<seg0>/subagents/<seg1>/subagents/<seg2>/…
```

`SessionDir` splits the ID on `--sub-`; one segment means a normal session and
the flat path is used unchanged. The 12 hex chars are the first 12 of the
delegation UUID with dashes stripped, so the mapping stays derived from the
delegation (a retried delegation resolves to the same folder) while keeping
the path short.

Why the separator is `--sub-` and not `-`, `.` or `~`: session IDs already
contain single dashes (UUIDs, channel-derived IDs), `~` is rejected by
`storage.ValidateSessionID`, and a bare `.` reads as a file extension when you
are looking at the folder. `--sub-` cannot occur in any ID wick generates.

**Path length.** Nesting plus 36-char UUIDs would push
`…/thinking/<turn>/<event>.json` past Windows' 260-char limit around depth 2.
Twelve hex chars per level keeps a depth-3 trace path near 240. Go's `os`
package also transparently applies the `\\?\` prefix for long absolute paths,
so this is headroom rather than the only defence.

### What nesting buys, for free

| Goal | How it is met |
|---|---|
| Cascade cleanup | `session.Delete` is `os.RemoveAll(SessionDir(id))` — descendants go with the parent. No orphan sweep. |
| Browsable by hand | Open the parent's folder, `subagents/` is right there. |
| Hidden from the session list | `session.List` is `ScanDirNames(SessionsDir())`, top-level only, so children can never appear. The existing `ParentSessionID` filter stays as belt-and-braces. |

### Who has to know about the nesting

Nesting removes sub-agents from every top-level scan, which is the point for
the sidebar and a bug everywhere else. `session.ListAll` walks the tree and is
used by the two callers that hydrate runtime state:

- the **boot registry** (`registry.Reload`) — otherwise a restarted wick cannot
  resolve a child by id at all, and its transcript 404s;
- the **session-workspace sweeper** — a sub-agent creates its own connector
  instances, and a scan that never sees the session never reaps them.

Everything that renders the conversation list keeps using `session.List`.

Two more registry seams, both pre-existing gaps that the modal would have made
visible: `delegation.PoolRunner` writes the child straight to disk rather than
through the registry manager, so it now publishes the session on create and
again after the agent entry, role prompt and active agent are written (a
message sent into the child resolves its agent name from the registry copy).
And `Registry.deleteSession` now drops the whole subtree, since deleting a
parent takes its children's folders with it.

### Migration

None. Sessions created before this change have IDs of the form `sub-<uuid>`,
which contain no `--sub-` separator, so `SessionDir` returns their existing
flat path. Old and new children coexist; nothing is moved or rewritten.

## The modal

Clicking a rail row opens `SubAgentModal` over the conversation.

```
┌──────────── main conversation (dimmed) ─────────────┐
│   ┌───────────────────── modal ──────────────────┐  │
│   │ Sub-agent · test-agent › researcher   [Stop] │  │  ← breadcrumb
│   │──────────────────────────────────────────────│  │
│   │ 🧠 thinking…                                 │  │
│   │ 🔧 read_file(internal/foo.go)      ▸ result  │  │
│   │ 💬 Saya Participant A…                       │  │
│   │──────────────────────────────────────────────│  │
│   │ [ ask this sub-agent…                    ⏎ ] │  │
│   └──────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

- **Transcript** reuses `ConversationThread` verbatim, fed by the existing
  generic `GET /api/sessions/{id}/conversation` and
  `GET /sessions/{id}/turns/{turn_id}` — a child is a normal session, so
  thinking blocks and `ToolCard`s render with zero new components.
- **Live updates** come from the existing SSE stream for that session ID.
- **Breadcrumb, not nested modals.** A child's own sub-agents push onto a
  breadcrumb stack inside the same modal; the crumb and Esc/Back pop it.
  Popping the last crumb closes the modal.
- **Stop** is the existing `POST /api/delegations/{id}/interrupt`, shown only
  while the delegation is `queued` or `running`.
- **Composer** posts to the generic `POST /sessions/{id}/send`, which spawns
  the child's agent again and appends to its own transcript. The delegation
  row is untouched: it stays `done`, and the leader keeps the result it was
  already handed. This is deliberately *not* `TakeOver` — take-over steers a
  run whose answer still has to go back to the leader, and so is gated on
  `AllowTakeOver` and flagged `UserSteered`. Asking a finished child a
  follow-up question changes nothing anyone else will read, so it needs
  neither gate nor flag.

## Follow-ups shipped alongside

- **The rail card is one click target.** The result preview at the bottom is
  the most informative part of a row and was the one dead zone; a `div` with
  `role="button"` (not a `<button>`, which cannot legally contain the Stop
  button) now opens the sub-agent from anywhere on the card.
- **Newest sub-agent first.** `ListByParent` ordered `started_at asc` while its
  own doc comment claimed newest-first. The rail is read top-down while work is
  in flight, so the delegation you just watched the leader make sank further
  down with every new one.
- **Sub-agents no longer leak into the sidebar.** Switching the boot registry to
  `ListAll` made children visible to `sidebarVMScoped`, which built the templ
  sidebar from `Registry().SessionIDs()` with no parent filter — they rendered
  as rows labelled with their parent's UUID. It now routes through
  `accessibleSessionIDs`, the same helper `/api/sessions` uses, so the two
  views cannot disagree about what a conversation is. Project chat counts
  exclude children for the same reason.
- **Liveness spins.** A static dot in a column of dots does not read as "this
  one is busy". Sidebar rows and rail cards show a spinner while working or
  spawning. The rail's spinner needs `lifecycle`, not `status`: a row can sit
  at `running` with nothing spawned (queued behind a slot), and spinning there
  promises progress that is not happening.
- **`GET /stream/sessions`** makes the sidebar live. Deliberately not the global
  `/stream`, which carries `pool_stats` listing every user's active sessions
  and is therefore admin-only; this one emits `{session_id, lifecycle}` for
  sessions that pass the caller's normal access check. The rail polls instead
  (3 s, only while something is live) because a sub-agent publishes lifecycle
  on the CHILD's session id, which the leader's stream is not subscribed to.
- **A dismissed autocomplete stays dismissed.** Unrelated to sub-agents, but it
  made the panel painful to use: `refreshMenu` runs on every keystroke, so the
  `@`/`/` popup reappeared on the character after Esc and covered the composer
  with "No matches". Dismissal is now remembered against the trigger position
  and clears when the token stops matching — a fresh trigger opens, editing the
  refused one does not.

## Testing

Go:
- `SessionDir` for a plain ID, depth-1, depth-2 and a legacy `sub-<uuid>` ID.
- `session.Create` on a child materialises the nested folder; `Load` finds it.
- `session.Delete` on the parent removes descendants.
- `run.go` derives a child ID that round-trips back to its parent.

Frontend (vitest + @testing-library/svelte):
- modal renders turns for the selected child.
- breadcrumb pushes on nested select and pops on Back, closing at the root.
- composer calls `sendMessage` with the child's session ID.
