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
        break;
      }

      case "thinking": {
        const lt = ensureLive();
        lt.blocks = [...lt.blocks, { kind: "thinking", text: ev.data ?? "" }];
        live.set(lt);
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
        turns.update((ts) => [...ts, systemTurn]);
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

      case "done":
      case "error": {
        finalize();
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
      turns.set(newTurns);
    },
    appendUserTurn,
    handleEvent,
  };
}
