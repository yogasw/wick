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
  appendUserTurn(text: string, attachments?: Attachment[]): void;
  handleEvent(ev: AgentEvent): void;
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
          typing.update((t) => ({ ...t, active: false }));
        } else if (lc === "spawning") {
          typing.set({ active: true, substate: "spawning" });
        } else if (lc === "working") {
          typing.set({ active: true, substate: ev.data ?? "" });
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

      case "thinking": {
        const lt = ensureLive();
        lt.blocks = [...lt.blocks, { kind: "thinking", text: ev.data ?? "" }];
        live.set(lt);
        markWorking();
        break;
      }

      case "tool_use": {
        const lt = ensureLive();
        const block: ThreadBlock = {
          kind: "tool",
          toolUseId: ev.tool_use_id ?? "",
          toolName: ev.tool_name ?? "",
          toolInput: ev.tool_input ?? "",
          startedAt: ev.at,
        };
        lt.blocks = [...lt.blocks, block];
        live.set(lt);
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
    setHistory(newTurns) {
      // The persisted history is authoritative. But a locally-committed
      // turn (live-*/error-*/warning-*) may not be in `newTurns` yet if the
      // reload raced the disk flush — dropping it would flicker the reply
      // out. So keep any local turn whose (role, text) is NOT already in the
      // persisted set, and drop the ones that ARE (their persisted twin
      // replaces them). This kills the intermittent double without losing a
      // reply when the reload lands early.
      turns.update((cur) => {
        const persistedKeys = new Set(newTurns.map((t) => `${t.role} ${t.text}`));
        const isLocal = (t: ConversationTurn) =>
          t.turn_id.startsWith("live-") ||
          t.turn_id.startsWith("error-") ||
          t.turn_id.startsWith("warning-") ||
          t.turn_id.startsWith("local-user-");
        const pendingLocal = cur.filter(
          (t) => isLocal(t) && !persistedKeys.has(`${t.role} ${t.text}`),
        );
        return [...newTurns, ...pendingLocal];
      });
    },
    appendUserTurn,
    handleEvent,
  };
}
