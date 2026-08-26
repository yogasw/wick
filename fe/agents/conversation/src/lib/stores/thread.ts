/*
 * Purpose:    Thread streaming store — reduces live AgentEvent stream into
 *             reactive thread state (turns, live turn, typing, lifecycle, meta).
 *             Mirrors agents.js handleAgentEvent state logic; no DOM.
 * Caller:     Conversation page / Slice 7 rendering layer
 * Dependencies: svelte/store, AgentEvent, ConversationTurn, LiveTurn,
 *               ThreadBlock, TypingState
 * Main Functions: createThreadStore
 * Side Effects: none
 */

import { writable, get } from "svelte/store";
import type { Writable } from "svelte/store";
import type {
  AgentEvent,
  Attachment,
  ConversationTurn,
  LiveTurn,
  Sender,
  ThreadBlock,
  TypingState,
} from "../types/agents.js";

export interface ThreadMeta {
  title?: string;
}

export type LifecycleState = {
  state: "spawning" | "working" | "idle" | "killed" | "";
  pid: number;
  substate: string;
  at: number;
};

export interface ThreadStore {
  turns: Writable<ConversationTurn[]>;
  live: Writable<LiveTurn | null>;
  typing: Writable<TypingState>;
  lifecycle: Writable<LifecycleState>;
  meta: Writable<ThreadMeta>;
  setHistory(turns: ConversationTurn[]): void;
  /* Insert an older history page (infinite scroll up) before the turns
     already loaded, dropping any turn whose id is already present. */
  prependHistory(older: ConversationTurn[]): void;
  appendUserTurn(text: string, attachments?: Attachment[]): void;
  handleEvent(ev: AgentEvent): void;
  /* Remove a stuck tool card from the live turn (a run with no runId to
     cancel — an orphan from before per-run cancel, or one whose finish event
     was lost). View cleanup only; the backend is not touched. */
  dismissToolBlock(toolUseId: string): void;
  /* Force the UI out of "thinking…"/live-turn state after a user-initiated
     kill, without waiting for a lifecycle:idle/killed SSE event. Needed
     because that event can be lost — the process may have already died
     silently (e.g. an idle-timeout race) before Kill was even clicked, or
     the SSE stream itself can drop the message — leaving the panel stuck
     showing "thinking…" with no visible effect from the Kill button. */
  handleKilledLocally(): void;
}

let _userTurnCounter = 0;

export function createThreadStore(): ThreadStore {
  const turns = writable<ConversationTurn[]>([]);
  const live = writable<LiveTurn | null>(null);
  const typing = writable<TypingState>({ active: false });
  const lifecycle = writable<LifecycleState>({ state: "", pid: 0, substate: "", at: 0 });
  const meta = writable<ThreadMeta>({});

  function markWorking(): void {
    lifecycle.update((l) =>
      l.state === "killed" || l.state === "working" ? l : { ...l, state: "working" }
    );
  }

  function ensureLive(): LiveTurn {
    let current = get(live);
    if (!current) {
      current = { text: "", blocks: [] };
      live.set(current);
    }
    return current;
  }

  function finalize() {
    const current = get(live);
    if (current && (current.text || current.blocks.length > 0)) {
      const assistantTurn: ConversationTurn = {
        turn_id: `live-${Date.now()}`,
        role: "assistant",
        agent: "",
        provider: "",
        text: current.text,
        timestamp: Date.now(),
        truncated: false,
        interrupted: false,
        has_trace: current.blocks.length > 0,
        events: current.blocks.map((b) => {
            if (b.kind === "tool") {
              return {
                type: "tool_use",
                tool_name: b.toolName,
                tool_input: b.toolInput,
                tool_use_id: b.toolUseId,
                is_error: b.isError,
                text: b.result,
              };
            }
            return { type: "thinking", text: b.text };
          }),
        attachments: [],
      };
      turns.update((ts) => [...ts, assistantTurn]);
    }
    live.set(null);
    typing.set({ active: false });
  }

  function handleEvent(ev: AgentEvent): void {
    switch (ev.type) {
      case "session_start": {
        typing.set({ active: true });
        break;
      }

      case "lifecycle": {
        const lc = ev.lifecycle ?? "";
        if (lc === "idle" || lc === "killed") {
          typing.update((t) => ({ ...t, active: false, toolName: undefined }));
        } else if (lc === "spawning") {
          typing.set({ active: true, substate: "spawning" });
        } else if (lc === "working") {
          typing.update((t) => ({ active: true, substate: ev.data ?? "", toolName: t.toolName }));
        }
        const lcState = (lc === "spawning" || lc === "working" || lc === "idle" || lc === "killed")
          ? lc as LifecycleState["state"]
          : "" as const;
        lifecycle.set({
          state: lcState,
          pid: ev.pid ?? 0,
          substate: ev.data ?? "",
          at: ev.at ?? 0,
        });
        break;
      }

      case "text_delta": {
        const lt = ensureLive();
        lt.text += ev.data ?? "";
        live.set(lt);
        typing.update((t) => ({ ...t, active: true }));
        markWorking();
        break;
      }

      // A full cumulative replay of the in-flight turn's text (sent by the
      // stream snapshot on every resubscribe — e.g. every page load while
      // the SharedWorker's stream is still open, not just after a real
      // gap). Replaces live.text instead of appending — treating it as a
      // delta would re-glue the same paragraph onto itself each reload.
      case "text_snapshot": {
        const lt = ensureLive();
        lt.text = ev.data ?? "";
        live.set(lt);
        typing.update((t) => ({ ...t, active: true }));
        markWorking();
        break;
      }

      case "thinking": {
        const lt = ensureLive();
        lt.blocks = [...lt.blocks, { kind: "thinking", text: ev.data ?? "" }];
        live.set(lt);
        markWorking();
        break;
      }

      case "tool_use": {
        const lt = ensureLive();
        const id = ev.tool_use_id ?? "";
        const block: ThreadBlock = {
          kind: "tool",
          toolUseId: id,
          toolName: ev.tool_name ?? "",
          toolInput: ev.tool_input ?? "",
          startedAt: ev.at,
        };
        // The stream snapshot (SharedWorker resubscribe on every page
        // load while a turn is in flight, not just after a real gap —
        // same cause as the text_snapshot duplication) replays every
        // InFlightEvents entry it has, including tool_use, each time.
        // Without a toolUseId dedup this appended a second identical
        // card for the same still-running tool call on every reload.
        const idx = id ? lt.blocks.findIndex((b) => b.kind === "tool" && b.toolUseId === id) : -1;
        if (idx >= 0) {
          lt.blocks = lt.blocks.map((b, i) => (i === idx ? block : b));
        } else {
          lt.blocks = [...lt.blocks, block];
        }
        live.set(lt);
        typing.update((t) => ({ ...t, active: true, toolName: ev.tool_name ?? "" }));
        markWorking();
        break;
      }

      case "tool_result": {
        const lt = ensureLive();
        const id = ev.tool_use_id ?? "";
        const idx = lt.blocks.findIndex(
          (b) => b.kind === "tool" && b.toolUseId === id
        );
        if (idx >= 0) {
          const updated = lt.blocks.map((b, i) => {
            if (i !== idx || b.kind !== "tool") return b;
            return {
              ...b,
              result: ev.data ?? "",
              isError: ev.is_error === true,
              endedAt: ev.at,
            };
          });
          lt.blocks = updated;
        } else {
          const standalone: ThreadBlock = {
            kind: "tool",
            toolUseId: id,
            toolName: "",
            toolInput: "",
            result: ev.data ?? "",
            isError: ev.is_error === true,
            endedAt: ev.at,
          };
          lt.blocks = [...lt.blocks, standalone];
        }
        live.set(lt);
        typing.update((t) => ({ ...t, toolName: undefined }));
        markWorking();
        break;
      }

      case "system_turn": {
        let text = "";
        let steps: string[] = [];
        try {
          const d = JSON.parse(ev.data ?? "{}") as { text?: string; steps?: string[] };
          text = d.text ?? "";
          steps = d.steps ?? [];
        } catch (_) {}
        const systemTurn: ConversationTurn = {
          turn_id: `sys-${Date.now()}`,
          role: "system",
          agent: "",
          provider: "",
          text,
          timestamp: Date.now(),
          truncated: false,
          interrupted: false,
          has_trace: false,
          events: steps.map((s) => ({ type: "step", text: s })),
          attachments: [],
        };
        // Collapse back-to-back switches live: if this is a provider-switch turn
        // and the tail of the thread is already switch turns (no real chat since),
        // drop them so only the latest remains — mirrors the backend's on-disk
        // prune so the UI doesn't stack a card per switch before the refetch.
        const isSwitch = (t: ConversationTurn) =>
          t.role === "system" && t.text.startsWith("Provider switched");
        if (isSwitch(systemTurn)) {
          turns.update((ts) => {
            let end = ts.length;
            while (end > 0 && isSwitch(ts[end - 1])) end--;
            return [...ts.slice(0, end), systemTurn];
          });
        } else {
          turns.update((ts) => [...ts, systemTurn]);
        }
        break;
      }

      case "user_message": {
        // A user turn injected from a non-web source (a channel or the
        // schedule runner). Web-sourced turns never arrive here (the pool
        // filters "ui" out) — those render optimistically via appendUserTurn.
        let text = "";
        let source = "";
        // Who sent it, when the channel resolved one. Carried on the event so
        // the live turn shows the same sender chip a reloaded one does.
        let sender: Sender | undefined;
        try {
          const d = JSON.parse(ev.data ?? "{}") as {
            text?: string;
            source?: string;
            sender?: Sender;
          };
          text = d.text ?? "";
          source = d.source ?? "";
          sender = d.sender;
        } catch (_) {}
        if (text.trim()) {
          const userTurn: ConversationTurn = {
            turn_id: `remote-user-${Date.now()}`,
            role: "user",
            agent: "",
            provider: "",
            text,
            source,
            sender,
            timestamp: Date.now(),
            truncated: false,
            interrupted: false,
            has_trace: false,
            events: [],
            attachments: [],
          };
          turns.update((ts) => [...ts, userTurn]);
        }
        break;
      }

      case "connector_run": {
        // A connector run started/finished under this session. Attach its run_id
        // + connector_id to the matching in-flight tool call so the card can show
        // a Cancel button; clear it when the run finishes. wick_execute is
        // synchronous and the event carries no tool_use_id, so we bind to the
        // most recent still-running tool block without a run id yet (start) or the
        // block already carrying this run id (finish).
        let d: { run_id?: string; connector_id?: string; running?: boolean };
        try {
          d = JSON.parse(ev.data ?? "{}");
        } catch (_) {
          break;
        }
        const runId = d.run_id ?? "";
        if (!runId) break;
        const lt = ensureLive();
        if (d.running) {
          // Bind to the last running tool block that doesn't have a run id yet.
          for (let i = lt.blocks.length - 1; i >= 0; i--) {
            const b = lt.blocks[i];
            if (b.kind === "tool" && b.result === undefined && !b.runId) {
              lt.blocks[i] = { ...b, runId, connectorId: d.connector_id ?? "" };
              break;
            }
          }
        } else {
          // Finished — drop the run id so the Cancel button disappears.
          lt.blocks = lt.blocks.map((b) =>
            b.kind === "tool" && b.runId === runId ? { ...b, runId: undefined, connectorId: undefined } : b,
          );
        }
        live.set(lt);
        break;
      }

      case "session_meta": {
        try {
          const d = JSON.parse(ev.data ?? "{}") as { session_id?: string; title?: string };
          const title = (d.title ?? "").trim();
          if (title) {
            meta.update((m) => ({ ...m, title }));
          }
        } catch (_) {}
        break;
      }

      case "done": {
        // End of turn: commit the streamed live turn. The caller reloads
        // the authoritative history right after; setHistory dedups the
        // just-committed live turn against its persisted twin so the reply
        // never renders twice (the race that caused intermittent doubles).
        finalize();
        break;
      }

      case "error":
      case "warning": {
        // A fatal error ends the turn (commit any partial text); a warning
        // is non-fatal and leaves the live turn running.
        if (ev.type === "error") finalize();
        // Surface the error/warning inline as a system error turn so it
        // shows immediately (matches persisted history on reload).
        const msg = (ev.data ?? "").trim();
        if (msg) {
          const errTurn: ConversationTurn = {
            turn_id: `${ev.type}-${Date.now()}`,
            role: "system",
            agent: "",
            provider: "",
            text: msg,
            timestamp: Date.now(),
            truncated: false,
            interrupted: false,
            has_trace: false,
            events: [],
            attachments: [],
            is_error: true,
          };
          turns.update((ts) => [...ts, errTurn]);
        }
        break;
      }

      /* approval_request, approval_resolved, ask_user, ask_user_resolved:
       * intentionally not handled here — they are fan-out to dedicated
       * approvals/asks stores wired by the App layer. */
      default:
        break;
    }
  }

  function appendUserTurn(text: string, attachments?: Attachment[]): void {
    const id = ++_userTurnCounter;
    const turn: ConversationTurn = {
      turn_id: `local-user-${id}`,
      role: "user",
      agent: "",
      provider: "",
      text,
      timestamp: Date.now(),
      truncated: false,
      interrupted: false,
      has_trace: false,
      events: [],
      attachments: attachments ?? [],
    };
    turns.update((ts) => [...ts, turn]);
  }

  return {
    turns,
    live,
    typing,
    lifecycle,
    meta,
    // dismissToolBlock removes a stuck tool card from the live turn (a run with
    // no runId to cancel — an orphan from before per-run cancel, or one whose
    // finish event was lost). Purely a view cleanup; the backend is untouched.
    dismissToolBlock(toolUseId: string) {
      const lt = get(live);
      if (!lt) return;
      const blocks = lt.blocks.filter(
        (b) => !(b.kind === "tool" && b.toolUseId === toolUseId),
      );
      live.set({ ...lt, blocks });
    },
    setHistory(newTurns) {
      // The persisted history is authoritative. But a locally-committed
      // turn (live-*/error-*/warning-*) may not be in `newTurns` yet if the
      // reload raced the disk flush — dropping it would flicker the reply
      // out. So keep any local turn whose (role, text) is NOT already in the
      // persisted set, and drop the ones that ARE (their persisted twin
      // replaces them). This kills the intermittent double without losing a
      // reply when the reload lands early.
      // Trace carry-over: the same reload-vs-flush race also hits the TRACE.
      // A persisted twin loads its trace lazily (has_trace=true, events=[] until
      // loadTrace), and right after `done` the turn's thinking/<id>.json may not
      // be flushed yet, so the twin arrives trace-less and the trace shown
      // mid-stream vanishes until a manual refresh. Meanwhile the local turn we
      // are about to drop still holds the full inline trace we streamed. So when
      // a persisted twin has no inline events but its local twin does, graft the
      // local events onto it.
      turns.update((cur) => {
        const isLocal = (t: ConversationTurn) =>
          t.turn_id.startsWith("live-") ||
          t.turn_id.startsWith("error-") ||
          t.turn_id.startsWith("warning-") ||
          t.turn_id.startsWith("local-user-");

        const localByKey = new Map<string, ConversationTurn>();
        for (const t of cur) {
          if (!isLocal(t)) continue;
          const key = `${t.role} ${t.text}`;
          if (!localByKey.has(key)) localByKey.set(key, t);
        }

        const merged = newTurns.map((t) => {
          if (t.events && t.events.length > 0) return t;
          const twin = localByKey.get(`${t.role} ${t.text}`);
          if (twin && twin.events && twin.events.length > 0) {
            return { ...t, events: twin.events, has_trace: true };
          }
          return t;
        });

        const persistedKeys = new Set(newTurns.map((t) => `${t.role} ${t.text}`));
        const pendingLocal = cur.filter(
          (t) => isLocal(t) && !persistedKeys.has(`${t.role} ${t.text}`),
        );
        // Pagination: `newTurns` may be only the LATEST window (limit=N), while
        // `cur` still holds older pages scrolled in via prependHistory. Keep
        // everything in `cur` that precedes the window's first turn; a full
        // (unpaginated) reload matches at index 0, so nothing is kept and the
        // old replace-all semantics hold.
        const firstId = newTurns[0]?.turn_id;
        const winStart = firstId ? cur.findIndex((t) => t.turn_id === firstId) : -1;
        const olderPrefix = winStart > 0 ? cur.slice(0, winStart) : [];

        return [...olderPrefix, ...merged, ...pendingLocal];
      });
    },
    prependHistory(older) {
      if (older.length === 0) return;
      turns.update((cur) => {
        // Turns persisted before turn_id existed have no id — they can't be
        // deduped, so always keep them (pages are disjoint ranges anyway).
        const have = new Set(cur.map((t) => t.turn_id).filter(Boolean));
        const fresh = older.filter((t) => !t.turn_id || !have.has(t.turn_id));
        return fresh.length > 0 ? [...fresh, ...cur] : cur;
      });
    },
    appendUserTurn,
    handleEvent,
    handleKilledLocally() {
      finalize();
      typing.set({ active: false });
      lifecycle.update((l) => (l.state === "killed" ? l : { ...l, state: "killed" }));
    },
  };
}
