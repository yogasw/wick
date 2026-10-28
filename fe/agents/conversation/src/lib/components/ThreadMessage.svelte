<script lang="ts">
  import type { Snippet } from "svelte";
  import { speakerLabel } from "../ticketSession.js";
  import TraceNote from "./TraceNote.svelte";
  import type { TraceFiles } from "../api/files.js";
  import type { ConversationTurn, ThreadBlock, TurnEvent, TurnEventPayload } from "../types/agents.js";
  import { renderMarkdown, linkifyText } from "../markdown.js";
  import { turnTime, parseEventTime } from "../timeFormat.js";
  import { enrich } from "../richRender.js";
  import { mergeTodoItemsWithSteps, stripTodoBlocks, latestTodoGoal } from "../todoGroups.js";
  import { avatarTone } from "../senderTone.js";
  import { isViewer, viewerName } from "../viewer.js";
  import { bareSlashCommand } from "../slashCommand.js";
  import { teamFrame } from "../teamMention.js";
  import { systemSource, foldable } from "../messageKind.js";
  import FoldToggle from "./FoldToggle.svelte";
  import { isSystemEventKind } from "../systemEvents.js";
  import SystemEventChip from "./system/SystemEventChip.svelte";
  import RemoteQueueChip from "./RemoteQueueChip.svelte";
  import InputRequestCard from "./system/InputRequestCard.svelte";
  import ProviderFailoverCard from "./system/ProviderFailoverCard.svelte";
  import ActionCard from "./system/ActionCard.svelte";
  import ApprovalRequestCard from "./system/ApprovalRequestCard.svelte";
  import type { ApprovalDecisionChoice } from "../interactiveCards.js";
  import { splitActionCards, cardMode } from "../actionCard.js";
  import type { CardState } from "../types/agents.js";
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import ToolCard from "./ToolCard.svelte";
  import DelegationBlock from "./team/DelegationBlock.svelte";
  import TeamTraceRows from "./team/TeamTraceRows.svelte";
  import { teamTraceOf, traceActive } from "../teamTrace.js";
  import { getTeamTasksCtx } from "../teamTasksContext.js";
  import { isTeamMessageTool, turnSpan } from "../delegations.js";
  import { interimGuards, interimNote, isInterimText } from "../teamResults.js";
  import TodoCard from "./TodoCard.svelte";
  import ArtifactGallery from "./ArtifactGallery.svelte";
  import MediaLightbox from "./MediaLightbox.svelte";
  import RemoteRecheck from "./RemoteRecheck.svelte";
  import type { RemoteRecheck as RecheckResult } from "../api/team.js";
  import { isRemoteTimeout, isLateReply, lateLabel, NOTE_NO_MARKER } from "../remoteRecheck.js";
  import { jumpLink, deliveryView } from "../slackDelivery.js";
  import { teammatesHiddenNote, TEAMMATES_HIDDEN_TIP } from "../teammatesBusy.js";
  import { replyable, replyPreview, type ReplyTarget } from "../replyTo.js";
  import { renderWithSourceTags } from "../sourceTags.js";
  import { splitWickPreamble } from "../wickPreamble.js";
  import type { ListenHold } from "../listenHolds.js";
  import { delayText } from "../api/listen.js";
  import HoverCard from "./HoverCard.svelte";

  type Props = {
    turn: ConversationTurn;
    loadTrace?: (turnId: string) => Promise<TurnEvent[]>;
    // Fetches the spilled payload of one large trace event (an index row
    // with large:true carries no text) — wired to
    // GET /sessions/{id}/turns/{turn_id}/events/{event_id}.
    loadTraceEvent?: (turnId: string, eventId: string) => Promise<TurnEventPayload>;
    // Fetches a stored trace binary (blob_ref) when its chip is clicked.
    loadTraceBlob?: (turnId: string, ref: string) => Promise<Blob>;
    traceFiles?: TraceFiles;
    /** Team agents by handle, for a teammate's avatar on its messages. */
    teamAgents?: Record<string, { id?: string; name: string; kind?: string; shape?: string; color?: string; expression?: string; image?: import("@wick-fe/common-avatar").AvatarImageView | null }>;
    /** Opens a Team agent's chat; unset (outside the Team app) the
        handoff row's target is plain text. */
    onOpenAgent?: (handle: string) => void;
    /** The agent this chat belongs to — names a speaker missing from teamAgents. */
    agent?: { handle?: string; name: string; kind?: string; shape?: string; color?: string; expression?: string; image?: import("@wick-fe/common-avatar").AvatarImageView | null; showTeammates?: boolean };
    /** The chat's agent has "Show teammates in channels" off (any view). */
    teammatesHidden?: boolean;
    /** Handle of the teammate a via-mention turn answered ("" = none). */
    via?: string;
    /** Server-computed actioncard state for the whole thread. */
    cards?: Record<string, CardState>;
    /** Posts an actioncard click back; unset = buttons stay inert. */
    onCardAction?: (cardId: string, value: string, label: string) => void;
    /** Settles an approval_request card through the gate. */
    onApprovalDecide?: (approvalId: string, decision: ApprovalDecisionChoice) => void;
    /** Reads a Slack remote turn's thread again ("Cek ulang"); unset = no
        button. Offered on a timeout and on a turn closed without marker. */
    onRemoteRecheck?: () => Promise<RecheckResult>;
    /** Cancels a message queued behind a busy remote agent. */
    onRemoteQueueCancel?: (queueId: string) => Promise<void>;
    /** Asks the agent to pick a recovered turn back up; unset = no
        button (only the thread's last turn gets one). */
    onContinue?: () => void;
    /** Starts a reply to this bubble in the composer; unset (read-only
        viewer, composer off) = no Reply action. */
    onReply?: (target: ReplyTarget) => void;
    /** Scrolls to the turn a reply quotes; unset = the quote is plain. */
    onJumpToTurn?: (turnId: string, fromTurnId?: string) => void;
    /** This bubble's quote lookup is in flight: spinner + "Finding message…". */
    jumpPending?: boolean;
    /** A later assistant answer exists in the thread (P60 S2 guard 2). */
    laterAnswer?: boolean;
    /** The Slack listen / auto-reply hold this user turn came from. */
    listenHold?: ListenHold;
    /** A group thread: the speaker row carries the clock and this (the
        "open in …'s chat" link) instead of each taking a line of its own. */
    speakerAside?: Snippet;
  };
  let { turn, teammatesHidden = false, loadTrace, loadTraceEvent, loadTraceBlob, traceFiles, teamAgents = {}, onOpenAgent, agent, via = "", cards = {}, onCardAction, onApprovalDecide, onRemoteRecheck, onRemoteQueueCancel, onContinue, onReply, onJumpToTurn, jumpPending = false, laterAnswer = false, listenHold, speakerAside }: Props = $props();

  /* Who spoke an assistant turn, from the server's turn.speaker — never
     guessed from the text. A turn answering a teammate's mention is nested
     under a "via @x" label so it reads as part of that exchange. */
  const speaker = $derived(turn.role === "assistant" ? turn.speaker : undefined);
  /* A ticket session's turn carries its owner (T12b): someone else's
     agent shares a handle with yours ("captain"), so it is matched by id
     and named "Name · @owner" instead of borrowing your agent's look. */
  const speakerMine = $derived(!speaker?.owner_id || teamAgents[speaker.handle]?.id === speaker.agent_id);
  const speakerAgent = $derived(
    speaker && speakerMine ? (teamAgents[speaker.handle] ?? (agent && agent.handle === speaker.handle ? agent : undefined)) : undefined,
  );
  const speakerName = $derived(speaker ? (speaker.owner_id ? (speakerMine && speakerAgent?.name) || speakerLabel(speaker, speakerMine) : speakerAgent?.name || "@" + speaker.handle) : "");
  const viaMention = $derived(speaker?.via === "mention");

  /* Only an agent's reply can hold a live actioncard; the server's `cards`
     state decides whether a fence IS one (see cardMode). */

  /* A teammate's turn (a "Message from" it sent, a task's "Reply from",
     a "Follow-up from", the "[team results …]" fan-in) reads as from that
     agent — a card on the left with its chip, the frame dropped — not as
     the person typing. */
  const teamFrom = $derived(isUserTurn(turn) ? teamFrame(turn.source, turn.text ?? "") : null);
  /* A turn a program posted (a schedule, a job's CLI report, a sub-agent's
     result, the API): no person typed it, so it is a card on the left too,
     never the green bubble on the right. */
  const systemSrc = $derived(isUserTurn(turn) && !teamFrom ? systemSource(turn.source) : null);
  /* A teammate's long message folds past a screenful; a program's card
     shows only its first line until opened. */
  let cardOpen = $state(false);
  const cardBody = $derived(teamFrom ? teamFrom.body : (turn.text ?? ""));
  const cardFolds = $derived(foldable(cardBody, 3, 240));
  /* The card's status: a dot and a muted word, colour only on the dot so
     "working" does not read as a warning. */
  const cardStatus = $derived.by(() => {
    if (!teamFrom) return null;
    if (teamFrom.kind === "followup") return { label: "follow-up", dot: "bg-link-400" };
    const st = (teamFrom.state || "").toLowerCase();
    if (!st) return null;
    if (st === "completed") return { label: st, dot: "bg-green-500" };
    if (st === "failed" || st === "rejected" || st === "canceled") return { label: st, dot: "bg-red-500" };
    if (st.includes("input")) return { label: "needs input", dot: "bg-link-400" };
    return { label: st, dot: "bg-warning-400" };
  });
  const teamFromAgent = $derived(teamFrom ? teamAgents[teamFrom.handle] : undefined);
  const isSystemEvent = $derived(turn.role === "system" && isSystemEventKind(turn.kind));
  const teamNames = $derived(Object.fromEntries(Object.entries(teamAgents).map(([h, a]) => [h, a.name])));
  function isUserTurn(t: ConversationTurn) {
    return t.role === "user";
  }

  const isUser = $derived(turn.role === "user");
  const isSystem = $derived(turn.role === "system");

  /* A reply the agent opened with [silent] is kept out of every channel and
     push alert by the backend, but still shows here so there's a visible
     record. Flag it (muted bell + dimmed) and strip the marker from display so
     the reader sees the content, not the plumbing. Case-insensitive, tolerant
     of leading whitespace — mirrors the server's looksSilent. */
  const isSilentReply = $derived(
    !isUser && !isSystem && /^\s*\[silent\]/i.test(turn.text ?? ""),
  );
  /* The marker also leaks when the agent puts it on a LATER line — a preamble,
     then a closing paragraph opened with [silent]. That turn is never
     suppressed (the flag above only reads the opening), but the plumbing still
     must not be shown, so line-opening markers are stripped too. One
     mid-sentence is the agent talking about the marker and is left alone. */
  const displayText = $derived(
    isUser || isSystem
      ? (turn.text ?? "")
      : (turn.text ?? "")
          .replace(/^\s*\[silent\]\s*/i, "")
          .replace(/^[ \t]*\[silent\][ \t]*/gim, ""),
  );

  /* A turn the server cut off with no author is one wick recovered from
     disk after a restart: the text is what the agent had written so far. */
  const RECOVERED_LABEL = "Recovered after a restart — this is what the agent had written before wick restarted.";

  /* "Interrupted — …" answers what happened and not the
     question that follows it: BY WHOM. A person clicking Stop, the agent
     stopping one of its own children, and wick going down mid-turn all
     landed on the same sentence, and only the last one means the work can
     be picked up again. The server names the author when it knows one; the
     bare wording stays for turns interrupted before it did. */
  const interruptedLabel = $derived.by(() => {
    const note = (turn.interrupted_note ?? "").trim();
    if (note) return isSystem ? `Stopped — ${note}` : `Interrupted — ${note}`;
    switch (turn.interrupted_by) {
      case "user":
        return "Interrupted — stopped from the conversation view";
      case "agent":
        return "Interrupted — stopped by the agent that started it";
      case "wick":
        return "Interrupted — wick stopped this process mid-turn";
      case "unknown":
        return "Stopped — nothing claimed it; check the daemon log for this time";
    }
    return RECOVERED_LABEL;
  });
  const isRecovered = $derived(
    !!turn.interrupted && turn.role === "assistant" && !turn.interrupted_by && !(turn.interrupted_note ?? "").trim(),
  );

  /* The server appends a "[routed] …" line to a person's message so the
     leader reads, in the same message, which @mentions wick already
     dispatched — without it the leader delegates them a second time. That
     line is plumbing addressed to the agent, not something the person
     typed, so the bubble keeps their words and the routing shows as a chip
     underneath. */
  const routed = $derived.by(() => {
    const raw = turn.text ?? "";
    if (!isUser) return { text: raw, note: "" };
    const at = raw.lastIndexOf("\n\n[routed]");
    const body = (t: string) => (teamFrom ? (teamFrame(turn.source, t)?.body ?? t) : t);
    if (at < 0) return { text: body(raw), note: "" };
    return { text: body(raw.slice(0, at)), note: raw.slice(at + 2) };
  });
  const routedHandles = $derived(routed.note.match(/@[a-z0-9-]+/g) ?? []);
  /* A Slack reply's [listen mode] note and thread catch-up block are for
     the agent: the bubble keeps the person's words, the rest folds into
     chips above it. */
  const preamble = $derived(isUser && !teamFrom ? splitWickPreamble(routed.text) : null);
  const bubbleText = $derived(preamble ? preamble.body : routed.text);
  let preambleOpen = $state<"" | "catchup">("");

  /* A held Slack reply's detail lives on its timestamp: when wick got it,
     how long it was held, where it went. The [listen mode] note gives the
     mode and hold when no listen_held row was linked (older history). */
  const heldCard = $derived.by(() => {
    if (!isUser || (!listenHold && !preamble?.listen)) return null;
    const auto = listenHold ? listenHold.mode === "auto_reply" : preamble?.listenMode === "auto";
    const held = listenHold?.heldFor || preamble?.listenLabel.match(/held (\S+)/)?.[1] || (listenHold?.delaySec ? delayText(listenHold.delaySec) : "");
    const at = listenHold?.startedMs ? new Date(listenHold.startedMs) : turn.ts ? new Date(turn.ts) : null;
    const rows: [string, string][] = [];
    if (at && !isNaN(at.getTime())) rows.push(["Received", at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })]);
    rows.push(["Held for", held || "not held"], ["Then", "Passed to the agent"], ["Via", "Slack"]);
    return { title: (auto ? "Auto-reply" : "Listen") + " · this message", rows };
  });
  let stampEl: HTMLSpanElement | undefined = $state();
  let stampHover = $state(false);

  /* A hold that never reached the agent: the server's reason, short. */
  const holdProblem = $derived(
    turn.kind === "listen_held"
      ? (turn.text ?? "").replace(/^(Auto-reply|Listen) · /, "").replace(/^not passed on: /, "Not passed to the agent: ").replace(/^dropped: /, "Dropped: ")
      : "",
  );

  /* Whether this user turn came from somebody other than the person reading.
     A session can be written into by several people — a colleague on the same
     dashboard, a teammate in Slack — and their bubbles are otherwise
     identical to yours, which is exactly the confusion this feature exists to
     remove. Matching on wick_user_id means the same human is recognised
     whether they typed here or in Slack. */
  const fromSomeoneElse = $derived(
    isUser && turn.sender != null && !isViewer(turn.sender.wick_user_id)
  );

  /* Badge above a user bubble. Shown when the message is from someone else
     (name, plus the channel when it did not come from the dashboard), or when
     it arrived from a non-web source at all — an unattributed Slack message
     still has to read as "not typed here".

     Your own messages get nothing: labelling every bubble you sent with your
     own name is noise, and the right-hand side already means "you".

     The name comes from the structured `sender` field, never from the message
     text, so nobody can put someone else's name on their own message. */
  const sourceBadge = $derived.by(() => {
    if (!isUser || teamFrom || systemSrc) return null;
    const src = (turn.source ?? "").trim().toLowerCase();

    const isWeb = src === "" || src === "ui" || src === "web";
    if (isWeb && !fromSomeoneElse) return null;

    const who = (turn.sender?.name || turn.sender?.handle || "").trim();
    const channelName = src.charAt(0).toUpperCase() + src.slice(1);
    // A colleague on the dashboard is named, not channel-labelled: "via Ui"
    // says nothing a reader wants. Off-web messages keep the channel, which
    // is genuinely useful context.
    const label = isWeb
      ? who
      : who
        ? `${who} · ${channelName}`
        : "via " + channelName;
    if (!label) return null;

    return {
      label,
      icon: src === "slack" || src === "telegram" ? src : "channel",
      // Split parts so the name can carry the visual weight and the channel
      // recede — the person is what a reader scans for in a busy thread.
      who,
      // Empty for a dashboard message: the label already omits it, and the
      // markup keys off this to decide whether to render the "· Channel" half.
      channelName: isWeb ? "" : channelName,
      // A one-letter avatar reads faster than any icon when several people
      // are talking: colour and initial are recognisable before the text is.
      initial: who ? who.trim().charAt(0).toUpperCase() : "",
      // Six fixed hues, picked from the sender ID so one person keeps the
      // same colour across every message and every reload. Hashing the ID
      // rather than the name means a display-name change doesn't reshuffle
      // it. Neutral-ish tones only — these must not read as status colours.
      tone: avatarTone(turn.sender?.id ?? who),
      title: turn.sender
        ? (who || turn.sender.id) +
          (isWeb ? "" : ` · via ${channelName}`) +
          (turn.sender.handle ? ` (@${turn.sender.handle})` : "")
        : `Sent via ${channelName}`,
    };
  });

  /* Per-bubble timestamp is just the clock (WhatsApp shows only HH:mm inside
     each bubble; the day/date lives in the centered separator the parent
     renders). Reads `ts` (RFC3339 from history) first, falls back to
     `timestamp` (epoch ms on client-built live turns). */
  const stamp = $derived(turnTime(turn));
  /* The speaker row already shows the clock (a group thread's speakerAside). */
  const stampOnSpeaker = $derived(!!speakerAside && !!speaker && !showMeta);

  /* Reply: who the chip names and what it previews. Display only — the
     server rebuilds both from the stored turn when the reply is sent. */
  const canReply = $derived(!!onReply && replyable(turn));
  function replyAuthor(): string {
    if (turn.role === "assistant") {
      return speakerName || agent?.name || turn.agent || "Assistant";
    }
    if (teamFrom) return teamFromAgent?.name || teamFrom.name;
    if (systemSrc) return systemSrc.label;
    if (fromSomeoneElse && sourceBadge?.who) return sourceBadge.who;
    // Same name the server stores on the reply (sender name), "You" only
    // for a bubble that has none yet.
    return turn.sender?.name || "You";
  }
  function startReply() {
    if (!onReply || !turn.turn_id) return;
    const text = turn.postback ? turn.postback.label || turn.postback.value : (turn.text ?? "");
    onReply({
      turnId: turn.turn_id,
      author: replyAuthor(),
      excerpt: replyPreview(text || turn.attachments?.[0]?.name || ""),
    });
  }

  /* Back to the Slack thread a message came from, and proof an agent reply
     made it there. Both are recorded by the server when the message passed
     through Slack; older turns simply have neither. */
  const sourceJump = $derived(isUser ? jumpLink(turn.sender?.permalink) : "");
  const delivery = $derived(turn.role === "assistant" ? deliveryView(turn.delivery) : null);

  const safeEvents = $derived(turn.events ?? []);
  const safeAttachments = $derived(turn.attachments ?? []);
  const safeArtifacts = $derived(turn.artifacts ?? []);
  const safeSteps = $derived(safeEvents.filter((ev) => ev.type === "step"));

  /* Compaction label: "Compacted 31.3k → 4.1k · manual". Built from
     Extras rather than parsed out of the text, so the numbers stay
     numbers — the text is only the plain-channel fallback. */
  const compactionLabel = $derived.by(() => {
    const x = turn.extras ?? {};
    const short = (v?: string) => {
      const n = Number(v ?? 0);
      if (!Number.isFinite(n) || n <= 0) return "";
      if (n < 1000) return String(n);
      if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`;
      return `${(n / 1_000_000).toFixed(2)}M`;
    };
    const pre = short(x.pre_tokens);
    const post = short(x.post_tokens);
    const how = x.trigger === "manual" ? "manual" : "auto";
    if (!pre || !post) return turn.text || "Context compacted";
    return `Compacted ${pre} → ${post} · ${how}`;
  });

  /* "/compact" on its own is an instruction to wick, not a sentence
     addressed to the agent — and the reply to it isn't an answer either.
     Rendering it as a green "you said" bubble makes the thread read as a
     conversation where none happened, so a bare command gets its own
     compact form instead. Anything with words around it stays a message:
     that text really was sent to the model. */
  const command = $derived(isUser ? bareSlashCommand(turn.text) : "");

  const showTraceToggle = $derived(!isUser && !isSystem && ((safeEvents.length > 0) || turn.has_trace === true));

  const isSyntheticId = $derived(
    turn.turn_id.startsWith("live-") || turn.turn_id.startsWith("sys-")
  );

  const canFetch = $derived(
    turn.has_trace === true && loadTrace !== undefined && !isSyntheticId
  );

  let traceOpen = $state(false);
  let traceLoading = $state(false);
  let traceEvents = $state<TurnEvent[] | null>(null);
  let traceError = $state(false);

  type LightboxItem = { url: string; name: string; kind: "image" | "pdf" | "html" | "markdown" | "text" | "file"; sourceUrl?: string };
  /* The viewer is always a gallery. A single attachment / artifact opens as a
     one-element gallery; an image-card grid opens with every card so the user
     can page through them (← / → / prev-next). */
  let lightbox = $state<{ items: LightboxItem[]; index: number } | null>(null);

  /* single-item entry point for attachments + artifacts (ArtifactGallery
     onOpen passes one item) — a one-element gallery. */
  function openLightbox(item: LightboxItem) {
    lightbox = { items: [item], index: 0 };
  }

  function openGallery(items: LightboxItem[], index: number) {
    if (items.length) lightbox = { items, index };
  }

  function closeLightbox() {
    lightbox = null;
  }

  /* The image-card grid is rendered imperatively by richRender (outside Svelte),
     so a clicked card can't call openGallery directly. It dispatches a
     `wick-imagecard-open` CustomEvent that bubbles to this bubble's root; we
     catch it here and open the gallery with the card's siblings. */
  function onImageCardOpen(e: Event) {
    const d = (e as CustomEvent).detail as { items?: LightboxItem[]; index?: number } | undefined;
    if (d?.items?.length) openGallery(d.items, d.index ?? 0);
  }

  const resolvedTraceEvents = $derived(traceEvents ?? safeEvents);

  /* Walk the trace in order, coalescing consecutive thinking deltas into one
     block so streamed thinking_delta fragments render as a single bubble
     instead of one bubble per delta. Tool calls break a thinking run, so
     chronological think→tool→think ordering is preserved. */
  const traceBlocks = $derived.by<ThreadBlock[]>(() => {
    const blocks: ThreadBlock[] = [];
    let thinking: string | null = null;
    const flush = () => {
      if (thinking !== null) {
        blocks.push({ kind: "thinking", text: thinking });
        thinking = null;
      }
    };
    for (const ev of resolvedTraceEvents) {
      if (ev.type === "thinking") {
        thinking = (thinking ?? "") + (ev.text ?? "");
      } else if (ev.type === "text") {
        flush();
        blocks.push({ kind: "text", text: ev.text ?? "" });
      } else if (ev.type === "tool_use") {
        flush();
        const res = resolvedTraceEvents.find((r) => r.type === "tool_result" && r.tool_use_id === ev.tool_use_id);
        blocks.push({
          kind: "tool",
          toolUseId: ev.tool_use_id ?? "",
          toolName: ev.tool_name ?? "",
          toolInput: ev.tool_input ?? "",
          // Big arguments spill the same way big results do — the sidecar
          // is keyed by the tool_use's own event_id.
          toolInputLarge: ev.large === true,
          toolInputSize: ev.size,
          toolInputEventId: ev.event_id,
          result: res?.text,
          // A large (spilled) result event has no text — the EVENT existing
          // is what marks the call finished, or the card spins forever.
          hasResult: res !== undefined,
          resultLarge: res?.large === true,
          resultSize: res?.size,
          resultEventId: res?.event_id,
          isError: res?.is_error,
          inputDisplay: ev.display,
          resultDisplay: res?.display,
          startedAt: parseEventTime(ev.at),
          endedAt: parseEventTime(res?.end_at ?? res?.at),
        });
      } else if (ev.type === "tool_result") {
        const paired = resolvedTraceEvents.some((u) => u.type === "tool_use" && u.tool_use_id === ev.tool_use_id);
        if (paired) {
          continue;
        }
        flush();
        blocks.push({
          kind: "tool",
          toolUseId: ev.tool_use_id ?? "",
          toolName: ev.tool_name ?? "tool result",
          toolInput: "",
          result: ev.text,
          hasResult: true,
          resultLarge: ev.large === true,
          resultSize: ev.size,
          resultEventId: ev.event_id,
          isError: ev.is_error,
          resultDisplay: ev.display,
          endedAt: parseEventTime(ev.end_at ?? ev.at),
        });
      } else if (ev.type === "raw") {
        // Unrecognized CLI frame kept for visibility — show as a raw block.
        flush();
        blocks.push({ kind: "raw", text: ev.text ?? "" });
      }
    }
    flush();
    return blocks;
  });

  // A long turn can call todo many times (checkpoint after checkpoint).
  // Merging every call into one checklist — instead of rendering a
  // separate card per call — means the widget always shows current
  // progress (% done) rather than stacking N nearly-identical cards.
  // The individual todo tool_use blocks are stripped from the flat
  // trace below since this merged widget replaces them.
  const mergedTodoItems = $derived(mergeTodoItemsWithSteps(traceBlocks));
  /* The turn's team_message calls are summed up in one delegation block
     (DelegationBlock) and recorded exactly in the trace (T16 S10): each
     call keeps its ToolCard there, with one nested row per teammate from
     the team_task events the server merges into the trace; a task an
     @handle line sent gets its rows after the trace. */
  const teamCtx = getTeamTasksCtx();
  const delegated = $derived(teamCtx && turn.role === "assistant" ? teamCtx.forTurn(turn.turn_id) : []);
  const nonTodoBlocks = $derived(stripTodoBlocks(traceBlocks));
  const teamTrace = $derived(teamTraceOf(resolvedTraceEvents));
  // The chat's live tasks by id: laid over the recorded rows (S2) and
  // what a row's Open opens.
  const liveById = $derived(Object.fromEntries((teamCtx?.tasks?.() ?? []).map((t) => [t.task_id, t])));
  const traceTasks = $derived([...[...teamTrace.byUse.values()].flat(), ...teamTrace.mentions]);
  /* While a recorded row's teammate still works, the stored trace is
     read again each time its task moves, so the row's thread grows; once
     every recorded row has settled it is left alone. */
  const liveTraceKey = $derived(traceTasks.map((t) => `${t.task_id}:${liveById[t.task_id]?.state ?? ""}:${liveById[t.task_id]?.updated_at ?? ""}`).join(" "));
  let traceReadKey = "";
  $effect(() => {
    const key = liveTraceKey;
    if (!traceOpen || traceEvents === null || !canFetch || !traceActive(traceTasks)) return;
    if (traceReadKey === "") { traceReadKey = key; return; }
    if (key === traceReadKey) return;
    traceReadKey = key;
    loadTrace!(turn.turn_id).then((ev) => { traceEvents = ev; }).catch(() => {});
  });
  // Goal mode is optional and independent of the checklist — a turn can
  // report a goal with no items at all, so the card renders for either.
  const todoGoal = $derived(latestTodoGoal(traceBlocks));

  /* grouped §3 asst-meta: a turn whose block shows opens with who
     answered and when ("Captain · 09:31 – 09:44", first task start to the
     last task end or this turn's commit; only the start while a task
     still runs). It stands in for the speaker
     chip and the stamp, so the turn names its time once. */
  const showMeta = $derived(delegated.length > 0 && !!teamCtx);
  const metaAgent = $derived(speakerAgent ?? agent);
  const metaSpan = $derived(showMeta ? turnSpan(delegated, turn.ts ? Date.parse(turn.ts) : turn.timestamp || NaN) : "");

  /* P60 S2: a turn that only delegated keeps its in-between text in the
     trace, not the body (isInterimText). A turn from history has no events
     until its trace is read: its text stays visible meanwhile (its tasks
     may be tasksByTurn's guess) and, when the guards would hide it, the
     trace is read once to confirm the tool calls. That read is kept apart
     from traceEvents, so opening the trace still fetches it fresh; a read
     that fails leaves the text where it is. */
  let interimEvents = $state<TurnEvent[] | null>(null);
  let interimReading = false;
  const decideEvents = $derived(traceEvents ?? interimEvents ?? safeEvents);
  const interimPending = $derived(
    turn.role === "assistant" && delegated.length > 0 && traceEvents === null && interimEvents === null && safeEvents.length === 0 && canFetch,
  );
  const interim = $derived(
    delegated.length > 0 && !interimPending &&
      isInterimText({ role: turn.role, text: turn.text, events: decideEvents }, delegated, laterAnswer),
  );
  $effect(() => {
    if (!interimPending || interimReading || !interimGuards(turn.text ?? "", delegated, laterAnswer)) return;
    interimReading = true;
    loadTrace!(turn.turn_id)
      .then((ev) => { interimEvents = ev; })
      .catch(() => { /* the text stays in the body */ });
  });
  const interimText = $derived(
    interim ? interimNote(turn.text ?? "", decideEvents.flatMap((e) => (e.type === "text" && e.text ? [e.text] : []))) : "",
  );

  async function toggleTrace() {
    if (traceOpen) {
      traceOpen = false;
      return;
    }
    if (traceEvents !== null || !canFetch) {
      traceOpen = true;
      return;
    }
    traceLoading = true;
    traceError = false;
    try {
      traceEvents = await loadTrace!(turn.turn_id);
      traceOpen = true;
    } catch {
      traceError = true;
    } finally {
      traceLoading = false;
    }
  }

  async function retryTrace() {
    traceError = false;
    traceEvents = null;
    await toggleTrace();
  }
  const cardSegments = $derived(
    turn.role === "assistant" ? splitActionCards(displayText) : [{ kind: "md" as const, text: displayText }],
  );
</script>

{#if isSystem}
  <div class="flex justify-center py-1">
    <div class="flex flex-col items-center gap-1 max-w-full">
      {#if turn.kind === "interrupted"}
        <!-- A stop that cut nothing off mid-sentence leaves no assistant turn
             to mark, so the stop itself is the record. Amber rather than the
             neutral system grey: this is why the answer you were waiting for
             never arrived, and it should not read like a routine notice. -->
        <div class="inline-flex items-start gap-1.5 rounded-2xl border border-amber-400/40 bg-amber-400/10 px-3 py-1 text-xs text-amber-700 dark:text-amber-300 max-w-full">
          <svg viewBox="0 0 16 16" class="h-3 w-3 mt-0.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M8 2L1.5 13.5h13L8 2z" stroke-linejoin="round"></path>
            <path d="M8 6v4M8 11.5v.5" stroke-linecap="round"></path>
          </svg>
          <span class="whitespace-pre-wrap break-words min-w-0">{interruptedLabel}</span>
        </div>
        {#if turn.extras?.remote_link}
          <!-- Stop only ended wick's listening: the remote has no cancel,
               so its own page is where the work is stopped. -->
          <a href={turn.extras.remote_link} target="_blank" rel="noopener noreferrer" class="text-[11px] text-amber-700 dark:text-amber-300 underline hover:no-underline break-all">Stop it on the remote: {turn.extras.remote_link}</a>
        {/if}
      {:else if turn.kind === "compaction"}
        <!-- Where older turns were folded into a summary. Rendered as a
             divider rather than a notice because that is what it is: the
             history above this line is no longer verbatim, and anyone
             reading upward needs to know before they trust it. The
             before/after pair carries the whole meaning, so both are on
             the line. -->
        <!-- The label is allowed to shrink. It used to be shrink-0, which
             is fine for "Compacted 42k → 8k · auto" and not at all fine for
             the fallback sentence the provider writes when the before/after
             numbers are missing — that one is long enough to push the whole
             thread wider than the window, which scrolled the conversation
             sideways under the rail. It truncates now; the full sentence is
             on the row's title. -->
        <div class="flex w-full items-center gap-2 overflow-hidden py-0.5" title={turn.text}>
          <div class="h-px flex-1 bg-white-300 dark:bg-navy-600"></div>
          <span class="inline-flex min-w-0 items-center gap-1.5 text-[11px] text-black-700 dark:text-black-600">
            <svg viewBox="0 0 12 12" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5">
              <path d="M2 3.5h8M3.5 6h5M5 8.5h2" stroke-linecap="round"></path>
            </svg>
            <span class="min-w-0 truncate">{compactionLabel}</span>
          </span>
          <div class="h-px flex-1 bg-white-300 dark:bg-navy-600"></div>
        </div>
      {:else if turn.kind === "remote_queue"}
        <RemoteQueueChip {turn} agentName={agent?.name ?? ""} onCancel={onRemoteQueueCancel} />
      {:else if turn.kind === "approval_request"}
        <ApprovalRequestCard {turn} onDecide={onApprovalDecide} />
      {:else if turn.kind === "input_request"}
        <InputRequestCard {turn} />
      {:else if turn.kind === "provider_failover" || turn.kind === "provider_return"}
        <ProviderFailoverCard {turn} />
      {:else if turn.kind === "listen_held"}
        <!-- Only a hold that ended badly reaches here (listenHolds.ts): the
             reply never got to the agent, so it stays visible, in red. No
             button: there is no API to hand a dropped reply over again. -->
        <div data-testid="listen-problem" class="inline-flex items-start gap-1.5 rounded-lg border border-neg-400/40 bg-neg-400/10 px-3 py-1 text-xs text-neg-400 max-w-full">
          <svg viewBox="0 0 16 16" class="h-3 w-3 mt-0.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" aria-hidden="true"><path d="M8.75 1.75 3.25 9.25h4.5l-.5 5 5.5-7.5h-4.5l.5-5z"></path></svg>
          <span class="whitespace-pre-wrap break-words min-w-0">{turn.extras?.mode === "auto_reply" ? "Auto-reply" : "Listen"}: {holdProblem}</span>
        </div>
      {:else if isSystemEvent}
        <!-- A server-recorded event (agent created, access changed, a Team
             handoff, a refused mention, …): one chip, drawn from the
             systemEvents registry. A handoff's target opens its own chat. -->
        <SystemEventChip {turn} names={teamNames} {onOpenAgent} />
      {:else if turn.is_error}
        <div class="inline-flex items-start gap-1.5 rounded-2xl border border-neg-400/40 bg-neg-400/10 px-3 py-1 text-xs text-neg-400 max-w-full">
          <svg viewBox="0 0 12 12" class="h-3 w-3 mt-0.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M6 1L11 10.5H1z" stroke-linejoin="round"></path>
            <path d="M6 5v2" stroke-linecap="round"></path>
            <circle cx="6" cy="8.6" r="0.4" fill="currentColor" stroke="none"></circle>
          </svg>
          <span class="whitespace-pre-wrap break-words min-w-0">{turn.text}</span>
        </div>
        {#if onRemoteRecheck && isRemoteTimeout(turn)}
          <RemoteRecheck onRecheck={onRemoteRecheck} />
        {/if}
      {:else}
        <div class="inline-flex items-start gap-1.5 rounded-2xl border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-3 py-1 text-xs text-black-700 dark:text-black-600 max-w-full">
          <svg viewBox="0 0 12 12" class="h-3 w-3 mt-0.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5">
            <circle cx="6" cy="6" r="4.5"></circle>
            <path d="M6 4v2l1 1" stroke-linecap="round"></path>
          </svg>
          <span class="whitespace-pre-wrap break-words min-w-0">{turn.text}</span>
        </div>
      {/if}
      {#if safeSteps.length > 0}
        <div class="flex flex-col items-center gap-0.5 mt-0.5">
          {#each safeSteps as ev}
            <div class="inline-flex items-center gap-1 text-xs text-black-600 dark:text-black-500">
              <svg viewBox="0 0 12 12" class="h-2.5 w-2.5 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M2 6l3 3 5-5" stroke-linecap="round" stroke-linejoin="round"></path>
              </svg>
              {ev.text ?? ""}
            </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>
{:else if isUser && turn.postback}
  <!-- A click on an actioncard button, recorded by the server: a small chip
       on the user's side, not a typed message. -->
  <div class="flex justify-end">
    <span data-testid="postback-chip" title={turn.text} class="inline-flex items-center gap-1 rounded-full bg-green-500/10 px-2.5 py-0.5 text-xs font-medium text-green-700 dark:text-green-300">✓ {turn.postback.label || turn.postback.value}</span>
  </div>
{:else if isUser && (teamFrom || systemSrc) && turn.text}
  <!-- A teammate's or a program's turn: a clean card on the surface colour,
       the width of an answer, on the left — avatar, name, time, then a
       dot + muted status and ↗ to the task. No accent line, no coloured
       chip: the surface and the sender tell it apart from an answer (no
       background) and from a person's message (green, on the right). -->
  <div class="flex min-w-0 justify-start group" data-testid={teamFrom ? "teammate-card" : "system-card"} data-kind={teamFrom ? teamFrom.kind : systemSrc?.key}>
    <div class="w-full min-w-0 rounded-xl bg-white-300 px-3 py-2.5 dark:bg-navy-700">
      <div class="flex min-w-0 items-center gap-2 text-[12.5px] leading-5">
        {#if teamFrom}
          {#if teamFrom.handle}
            <AgentAvatar kind={teamFromAgent?.kind} image={teamFromAgent?.image} shape={teamFromAgent?.shape} expression={teamFromAgent?.expression} color={teamFromAgent?.color} size={20} />
          {:else}
            <span aria-hidden="true" class="grid h-5 w-5 shrink-0 place-items-center rounded-full bg-white-100 text-[11px] dark:bg-navy-600">🤝</span>
          {/if}
          <span data-testid="team-sender-chip" title={teamFrom.handle ? `${teamFrom.name} (@${teamFrom.handle}) · Team agent` : "Replies from several teammates"} class="min-w-0 truncate font-semibold text-black-900 dark:text-white-100">{teamFromAgent?.name || teamFrom.name}</span>
        {:else if systemSrc}
          <span aria-hidden="true" class="grid h-5 w-5 shrink-0 place-items-center rounded-full bg-white-100 text-[11px] dark:bg-navy-600">{systemSrc.icon}</span>
          <span data-testid="source-chip" title={"Posted by " + systemSrc.label + ", not typed by a person"} class="min-w-0 truncate font-semibold text-black-900 dark:text-white-100">{systemSrc.label === "cli" ? "CLI" : systemSrc.label.charAt(0).toUpperCase() + systemSrc.label.slice(1)}</span>
        {/if}
        {#if teamFrom?.kind === "revision"}
          <!-- A revision of a task (amend_task): one muted line, no status —
               it is the same task, not a new one. -->
          <span data-testid="card-revision" title={"Revision of task " + teamFrom.taskId} class="shrink-0 text-[11.5px] text-black-800 dark:text-black-600">↻ revision {teamFrom.revision}{teamFrom.replace ? " · replaces the brief" : ""}{stamp ? " · " + stamp : ""}</span>
        {:else if stamp}
          <span class="shrink-0 text-[11.5px] text-black-800 dark:text-black-600">{stamp}</span>
        {/if}
        {#if canReply}
          <!-- Reply sits by the time, as under an answer, so the card has no
               extra row for it and it never stacks on "Show more". -->
          <button
            type="button"
            data-testid="reply-action"
            aria-label="Reply to this message"
            title="Reply"
            onclick={startReply}
            class="inline-flex shrink-0 items-center gap-0.5 rounded px-1 text-[11.5px] leading-none text-black-800 opacity-0 transition-opacity hover:text-green-700 focus-visible:opacity-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-green-500 group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100 dark:text-black-600 dark:hover:text-green-400"
          >↩ Reply</button>
        {/if}
        <span class="flex-1"></span>
        {#if teamFrom && cardStatus}
          <span data-testid="card-status" class="inline-flex shrink-0 items-center gap-1.5 text-[11.5px] text-black-800 dark:text-black-600">
            <span aria-hidden="true" class={"h-[7px] w-[7px] rounded-full " + cardStatus.dot}></span>{cardStatus.label}
          </span>
        {/if}
        {#if teamFrom?.handle && onOpenAgent}
          <button type="button" data-testid="card-open" onclick={() => onOpenAgent?.(teamFrom.handle)} title={teamFrom.taskId ? "Open task " + teamFrom.taskId : `Open the chat with @${teamFrom.handle}`} aria-label={teamFrom.taskId ? "Open task " + teamFrom.taskId : `Open the chat with @${teamFrom.handle}`} class="shrink-0 rounded-md px-1 text-[13px] text-black-800 hover:bg-white-400 focus-visible:outline focus-visible:outline-2 focus-visible:outline-green-500 dark:text-black-600 dark:hover:bg-navy-600">↗</button>
        {/if}
      </div>
      {#if teamFrom}
        <div
          use:enrich={cardBody}
          class={"wick-prose wick-prose-sm mt-0.5 min-w-0 break-words text-black-900 dark:text-white-100 [&_code]:bg-white-100 dark:[&_code]:bg-navy-600" + (cardFolds && !cardOpen ? " max-h-[4.8em] overflow-hidden" : "")}
        >
          {@html renderMarkdown(cardBody)}
        </div>
      {:else}
        <div data-testid="system-card-body" class={"mt-0.5 min-w-0 whitespace-pre-wrap text-[13px] leading-relaxed text-black-800 [overflow-wrap:anywhere] dark:text-black-600" + (cardFolds && !cardOpen ? " line-clamp-3" : "")}>{@html linkifyText(cardBody)}</div>
      {/if}
      {#if cardFolds}
        <FoldToggle open={cardOpen} testid="card-fold" onclick={() => (cardOpen = !cardOpen)} />
      {/if}
    </div>
  </div>
{:else if isUser}
  <!-- Only the bubble the user just sent (optimistic local turn) slides in;
       history and refreshes render still. -->
  <!-- A teammate agent's message sits on the LEFT, where replies live: the
       right-hand side means "a person typed this", and an agent writing into
       the thread is not that. -->
  <div class={"flex min-w-0 max-w-full gap-2 group " + (teamFrom ? "justify-start" : "justify-end")} class:wick-enter-up={turn.turn_id?.startsWith("local-user-")}>
    <div class={"flex flex-col gap-1 min-w-0 max-w-[90%] sm:max-w-[80%] lg:max-w-[44rem] " + (teamFrom ? "items-start" : "items-end")}>
      {#if safeAttachments.length > 0}
        <div class={"flex flex-wrap gap-1.5 max-w-full " + (teamFrom ? "justify-start" : "justify-end")}>
          {#each safeAttachments as attachment}
            {#if attachment.mime?.startsWith("image/")}
              <button
                type="button"
                data-lightbox-trigger
                title={attachment.name}
                onclick={() => openLightbox({ url: attachment.url, name: attachment.name, kind: "image" })}
                class="block rounded-xl overflow-hidden border border-white-300 dark:border-navy-600 shadow-sm bg-white-100 dark:bg-navy-800 hover:shadow-md transition-shadow cursor-zoom-in"
              >
                <img src={attachment.url} alt={attachment.name} class="block max-h-56 max-w-[240px] object-contain bg-white-200 dark:bg-navy-900" />
              </button>
            {:else}
              <a
                href={attachment.url}
                target="_blank"
                rel="noopener"
                class="inline-flex items-center gap-2 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-xs text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors max-w-[240px] text-left"
              >
                <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5">
                  <path d="M9 2H4a1 1 0 00-1 1v10a1 1 0 001 1h8a1 1 0 001-1V6L9 2z" stroke-linejoin="round"></path>
                  <path d="M9 2v4h4" stroke-linejoin="round"></path>
                </svg>
                <span class="truncate">{attachment.name}</span>
              </a>
            {/if}
          {/each}
        </div>
      {/if}
      {#if turn.text}
        <!-- chip + bubble are one tight unit: the source chip sits flush on
             top of the bubble, tinted to match, so it reads as part of the
             message rather than a floating label. -->
        <div class={"flex flex-col gap-0.5 min-w-0 max-w-full " + (teamFrom ? "items-start" : "items-end")}>
          {#if teamFrom}
            <span
              data-testid="team-sender-chip"
              title={`${teamFrom.name} (@${teamFrom.handle}) · Team agent`}
              class="inline-flex items-center gap-1.5 pl-1 ml-0.5 text-[11px] leading-4 text-black-800 dark:text-black-600"
            >
              <AgentAvatar kind={teamFromAgent?.kind} image={teamFromAgent?.image} shape={teamFromAgent?.shape} expression={teamFromAgent?.expression} color={teamFromAgent?.color} size={20} />
              <span class="min-w-0 truncate"
                ><span class="font-medium text-black-900 dark:text-white-100">{teamFromAgent?.name || teamFrom.name}</span
                ><span class="opacity-70">{" · @" + teamFrom.handle}</span></span
              >
            </span>
          {/if}
          {#if sourceBadge}
            <span
              data-testid="sender-chip"
              title={sourceBadge.title}
              class="inline-flex items-center gap-1.5 pr-1 mr-0.5 text-[11px] leading-4 text-black-800 dark:text-black-600"
            >
              {#if sourceBadge.initial}
                <!-- One-letter avatar in a colour stable per person: in a busy
                     thread you recognise the speaker before reading the name. -->
                <span
                  aria-hidden="true"
                  class={"flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[9px] font-semibold " +
                    sourceBadge.tone.cls}
                >{sourceBadge.initial}</span>
              {/if}

              {#if sourceBadge.icon === "clock"}
                <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 opacity-70" fill="none" stroke="currentColor" stroke-width="2">
                  <circle cx="8" cy="9" r="5.5"></circle>
                  <path d="M8 6v3l2 1.5M3 2.5 1.5 4M13 2.5 14.5 4" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              {:else if sourceBadge.icon === "slack"}
                <!-- Slack's four-bar mark, monochrome so it inherits the muted
                     chip colour instead of competing with the message. -->
                <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 opacity-70" fill="currentColor" aria-hidden="true">
                  <path d="M3.4 9.9a1.4 1.4 0 1 1-1.4-1.4h1.4v1.4Zm.7 0a1.4 1.4 0 1 1 2.8 0v3.5a1.4 1.4 0 1 1-2.8 0V9.9Z"></path>
                  <path d="M6.5 3.4A1.4 1.4 0 1 1 7.9 2v1.4H6.5Zm0 .7a1.4 1.4 0 1 1 0 2.8H3a1.4 1.4 0 1 1 0-2.8h3.5Z"></path>
                  <path d="M12.6 6.5a1.4 1.4 0 1 1 1.4 1.4h-1.4V6.5Zm-.7 0a1.4 1.4 0 1 1-2.8 0V3a1.4 1.4 0 1 1 2.8 0v3.5Z"></path>
                  <path d="M9.5 12.6a1.4 1.4 0 1 1-1.4 1.4v-1.4h1.4Zm0-.7a1.4 1.4 0 1 1 0-2.8H13a1.4 1.4 0 1 1 0 2.8H9.5Z"></path>
                </svg>
              {:else if sourceBadge.icon === "telegram"}
                <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 opacity-70" fill="currentColor" aria-hidden="true">
                  <path d="M14.5 2.3 1.9 7.2c-.7.3-.7.8 0 1l3.2 1 1.2 3.7c.2.4.3.6.6.6.3 0 .4-.1.6-.3l1.5-1.5 3.1 2.3c.6.3 1 .1 1.1-.5l2.1-9.8c.2-.8-.3-1.1-.8-.9ZM6.3 9.4l6.4-4c.3-.2.6 0 .3.2L7.9 10.4l-.2 2.1-1.4-3.1Z"></path>
                </svg>
              {:else}
                <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 opacity-70" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M3 4a1 1 0 011-1h8a1 1 0 011 1v6a1 1 0 01-1 1H6l-3 2.5V4z" stroke-linejoin="round"></path>
                </svg>
              {/if}

              {#if sourceBadge.who}
                <!-- The person carries the weight; the channel recedes. Both
                     stay in one text node so a screen reader (and the tests)
                     read "Name · Channel" as a single label. -->
                <span class="min-w-0 truncate"
                  ><span class="font-medium text-black-900 dark:text-white-100">{sourceBadge.who}</span
                  >{#if sourceBadge.channelName && sourceJump}<span class="opacity-70">{" · "}</span><a
                      data-testid="jump-to-thread"
                      href={sourceJump}
                      target="_blank"
                      rel="noopener noreferrer"
                      title="Open this message's thread in Slack"
                      class="inline-flex items-center gap-0.5 rounded opacity-70 hover:underline hover:opacity-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-green-500"
                      >{sourceBadge.channelName}<svg viewBox="0 0 16 16" class="h-2.5 w-2.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M6 3H3v10h10v-3M9 3h4v4M13 3 7 9" stroke-linecap="round" stroke-linejoin="round"></path></svg></a
                    >{:else if sourceBadge.channelName}<span class="opacity-70">{" · " + sourceBadge.channelName}</span>{/if}</span
                >
              {:else}
                <span class="truncate">{sourceBadge.label}</span>
              {/if}
            </span>
          {/if}
          {#if sourceJump && !(sourceBadge?.who && sourceBadge.channelName)}
            <!-- No "Name · Slack" label to hang the link on: a bare icon, so
                 the jump never costs a line of its own. -->
            <a
              data-testid="jump-to-thread"
              href={sourceJump}
              target="_blank"
              rel="noopener noreferrer"
              title="Open this message's thread in Slack"
              aria-label="Open this message's thread in Slack"
              class="inline-flex shrink-0 items-center rounded p-1 -m-1 text-black-500 hover:text-green-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-green-500 dark:text-black-600 dark:hover:text-green-400"
            >
              <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                <path d="M6 3H3v10h10v-3M9 3h4v4M13 3 7 9" stroke-linecap="round" stroke-linejoin="round"></path>
              </svg>
            </a>
          {/if}
          {#if turn.reply_to}
            <!-- What this message answers: author + one line, built by the
                 server. Clicking jumps to the original. -->
            {@const q = turn.reply_to}
            <button
              type="button"
              data-testid="reply-quote"
              disabled={!onJumpToTurn}
              onclick={() => onJumpToTurn?.(q.turn_id, turn.turn_id)}
              title={jumpPending ? "Finding message…" : onJumpToTurn ? "Jump to the original message" : undefined}
              aria-busy={jumpPending || undefined}
              aria-label={`In reply to ${q.author || "a message"}: ${replyPreview(q.excerpt)}${onJumpToTurn ? ". Jump to original" : ""}`}
              class="flex min-w-0 max-w-full flex-col items-start rounded-lg border-l-2 border-green-500 bg-white-200 px-2.5 py-1 text-left text-xs text-black-700 enabled:hover:bg-white-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-green-500 dark:bg-navy-800 dark:text-black-600 dark:enabled:hover:bg-navy-700"
            >
              <span class="inline-flex items-center gap-1.5 font-medium text-black-900 dark:text-white-100">
                {q.author || "Message"}
                {#if jumpPending}
                  <svg data-testid="reply-quote-spinner" viewBox="0 0 16 16" class="h-3 w-3 animate-spin text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true"><path d="M8 2a6 6 0 106 6" stroke-linecap="round"/></svg>
                {/if}
              </span>
              <span class="max-w-full truncate">{replyPreview(q.excerpt)}</span>
            </button>
          {/if}
          <!-- The [listen mode] note has no chip: its detail is on the
               timestamp below the bubble (heldCard). -->
          {#if preamble && preamble.catchUp}
            <div class="flex max-w-full flex-col items-end gap-1" data-testid="wick-preamble">
              <div class="flex flex-wrap justify-end gap-1">
                {#if preamble.catchUp}
                  <button type="button" data-testid="catchup-chip" aria-expanded={preambleOpen === "catchup"}
                    onclick={() => (preambleOpen = preambleOpen === "catchup" ? "" : "catchup")}
                    class="inline-flex items-center gap-1 rounded-full border border-white-400 bg-white-200 px-2 py-0.5 text-[11px] text-black-800 dark:border-navy-600 dark:bg-navy-800 dark:text-black-600"
                  >🧵 {preamble.catchUpCount || ""} {preamble.catchUpCount === 1 ? "message" : "messages"} in the thread</button>
                {/if}
              </div>
              {#if preambleOpen}
                <div data-testid="wick-preamble-detail" class="max-h-60 max-w-full overflow-y-auto whitespace-pre-wrap rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-left text-xs text-black-800 [overflow-wrap:anywhere] dark:border-navy-600 dark:bg-navy-800 dark:text-black-600">{preamble.catchUp}</div>
              {/if}
            </div>
          {/if}
          {#if command}
            <!-- A command, not a line of chat: monospace on a quiet surface
                 with a prompt caret, so it reads as something that was RUN.
                 Deliberately small — the interesting part is what it did,
                 which lands below it. -->
            <span
              data-testid="command-chip"
              title="Slash command sent to the agent"
              class="inline-flex items-center gap-1.5 rounded-lg border border-green-500/40 bg-green-500/10 px-2.5 py-1 font-mono text-xs text-green-700 dark:text-green-300"
            >
              <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 opacity-80" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true">
                <path d="M4 4.5 7 8l-3 3.5M9 11.5h3.5" stroke-linecap="round" stroke-linejoin="round"></path>
              </svg>
              /{command}
            </span>
          {:else if bubbleText}
          <!-- Green is "you". Someone else writing into this session gets a
               neutral surface instead, so the two are distinguishable at a
               glance rather than only by reading the name above them. -->
          <div
            class={"min-w-0 max-w-full overflow-hidden rounded-2xl px-4 py-2.5 text-base whitespace-pre-wrap [overflow-wrap:anywhere] leading-relaxed shadow-sm " +
              (teamFrom ? "rounded-tl-sm " : "rounded-br-sm ") +
              (fromSomeoneElse || teamFrom
                ? "bg-white-200 dark:bg-navy-700 text-black-900 dark:text-white-100 ring-1 ring-white-400 dark:ring-navy-600"
                : "bg-green-500 text-white-100")}
          >
            {@html linkifyText(bubbleText)}
          </div>
          {/if}
        </div>
        {#if routedHandles.length > 0}
          <span
            data-testid="routed-chip"
            title="wick dispatched these sub-agents for this message"
            class="inline-flex items-center gap-1 rounded-full bg-white-200 dark:bg-navy-800 px-2 py-0.5 text-[10px] font-medium text-black-600 dark:text-black-500"
          >
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5">
              <path d="M2 4h5l2 2h5M13 6v6H3" stroke-linecap="round" stroke-linejoin="round"></path>
            </svg>
            Routed to {routedHandles.join(", ")}
          </span>
        {/if}
        {#if stamp || canReply}
          <span class={"flex items-center gap-1 " + (teamFrom ? "self-start" : "self-end")}>
          {#if stamp && heldCard}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <span bind:this={stampEl} data-testid="held-stamp" tabindex="0" role="note" aria-label={heldCard.title}
            onmouseenter={() => (stampHover = true)} onmouseleave={() => (stampHover = false)}
            onfocus={() => (stampHover = true)} onblur={() => (stampHover = false)}
            class="cursor-help border-b border-dashed border-black-500 text-[10px] leading-none text-black-500 hover:border-black-900 hover:text-black-900 dark:border-black-600 dark:text-black-600 dark:hover:border-white-100 dark:hover:text-white-100">{stamp}</span>
          {#if stampHover}<HoverCard anchor={stampEl} title={heldCard.title} rows={heldCard.rows} testid="held-card" />{/if}
          {:else if stamp}
          <span class="text-[10px] leading-none text-black-500 dark:text-black-600 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">{stamp}</span>
          {/if}
          {#if canReply}
            <button
              type="button"
              data-testid="reply-action"
              aria-label="Reply to this message"
              title="Reply"
              onclick={startReply}
              class="inline-flex items-center gap-0.5 rounded px-1 text-[10px] leading-none text-black-500 opacity-0 transition-opacity hover:text-green-700 focus-visible:opacity-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-green-500 group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100 dark:text-black-600 dark:hover:text-green-400"
            >
              <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M6.5 4 2.5 8l4 4M3 8h6.5a4 4 0 0 1 4 4v1" stroke-linecap="round" stroke-linejoin="round"></path></svg>
              Reply
            </button>
          {/if}
          </span>
        {/if}
      {/if}
    </div>
  </div>
{:else}
  <div class={"flex justify-start group" + (viaMention ? " ml-3 pl-3 border-l-2 border-white-300 dark:border-navy-600" : "")} data-via={viaMention ? "mention" : undefined}>
    <div class="flex flex-col gap-1.5 w-full max-w-full min-w-0">
      {#if showMeta}
        <div data-testid="asst-meta" class="flex items-center gap-1.5 text-[11px] text-black-700 dark:text-black-600">
          <AgentAvatar kind={metaAgent?.kind} image={metaAgent?.image} shape={metaAgent?.shape} expression={metaAgent?.expression} color={metaAgent?.color} size={18} />
          <span class="min-w-0 truncate">{replyAuthor()}{viaMention ? (via ? " · via @" + via : " · via mention") : ""}{metaSpan ? " · " + metaSpan : ""}</span>
        </div>
      {:else if speaker}
        <span data-testid="speaker-chip" class="inline-flex items-center gap-1.5 self-start text-[11px] leading-4 text-black-800 dark:text-black-600">
          <AgentAvatar kind={speakerAgent?.kind} image={speakerAgent?.image} shape={speakerAgent?.shape} expression={speakerAgent?.expression} color={speakerAgent?.color} size={20} />
          <span class="min-w-0 truncate"
            ><span class="font-medium text-black-900 dark:text-white-100">{speakerName}</span
            >{#if viaMention}<span class="opacity-70">{via ? " · via @" + via : " · via mention"}</span>{/if}</span
          >
          {#if speakerAside}
            {#if stamp}<span class="shrink-0 opacity-70">{stamp}</span>{/if}
            {@render speakerAside()}
          {/if}
        </span>
      {/if}
      {#if showTraceToggle}
        <div class="flex flex-col gap-1">
          {#if mergedTodoItems.length > 0 || todoGoal}
            <!-- Task progress, not raw trace detail — shown regardless of
                 traceOpen so collapsing the trace never hides "what was
                 done" (this turn is already finished, so there's no
                 currentActivity to show, unlike the live turn). Also
                 renders for a goal-only turn, which has no items. -->
            <TodoCard items={mergedTodoItems} goal={todoGoal} />
          {/if}
          <button
            type="button"
            onclick={toggleTrace}
            class="inline-flex items-center gap-1.5 self-start text-[11px] text-black-500 dark:text-black-600 hover:text-black-700 dark:hover:text-black-500 transition-colors"
          >
            {#if traceLoading}
              <svg class="h-3 w-3 shrink-0 animate-spin" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
              </svg>
              <span>loading…</span>
            {:else}
              <span>{traceOpen ? "⊖" : "⊕"}</span>
              <span>{traceOpen ? "hide trace" : "show trace ›"}</span>
            {/if}
          </button>

          {#if traceError}
            <div class="flex items-center gap-2 text-[11px] text-red-600 dark:text-red-400">
              <span>failed to load trace</span>
              <button
                type="button"
                onclick={retryTrace}
                class="underline hover:no-underline"
              >retry</button>
            </div>
          {/if}

          {#if traceOpen}
            <div class="flex flex-col gap-1 mt-0.5" data-trace-blocks>
              {#each nonTodoBlocks as block}
                {#if block.kind === "thinking" || block.kind === "text"}
                  <TraceNote kind={block.kind} text={block.text} />
                {:else if block.kind === "raw"}
                  <details class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 overflow-hidden text-xs">
                    <summary class="cursor-pointer px-3 py-2 text-black-600 dark:text-black-700 select-none">Raw event</summary>
                    <pre class="px-3 pb-2 overflow-x-auto text-[11px] text-black-700 dark:text-black-600 whitespace-pre-wrap break-words">{block.text}</pre>
                  </details>
                {:else}
                  <ToolCard
                    {block}
                    interrupted={turn.interrupted}
                    loadEventPayload={loadTraceEvent && !isSyntheticId
                      ? (eventId) => loadTraceEvent!(turn.turn_id, eventId)
                      : undefined}
                    loadBlob={loadTraceBlob && !isSyntheticId
                      ? (ref) => loadTraceBlob!(turn.turn_id, ref)
                      : undefined}
                    {traceFiles}
                  />
                  {#if isTeamMessageTool(block.toolName) && teamTrace.byUse.get(block.toolUseId)?.length}
                    <TeamTraceRows tasks={teamTrace.byUse.get(block.toolUseId) ?? []} captainName={teamCtx?.captainName()} live={liveById} onOpen={teamCtx?.openChat} />
                  {/if}
                {/if}
              {/each}
              {#if interimText}
                <TraceNote kind="text" text={interimText} />
              {/if}
              {#if teamTrace.mentions.length > 0}
                <TeamTraceRows tasks={teamTrace.mentions} mention captainName={teamCtx?.captainName()} live={liveById} onOpen={teamCtx?.openChat} />
              {/if}
            </div>
          {/if}
        </div>
      {/if}

      {#if delegated.length > 0 && teamCtx}
        <DelegationBlock
          tasks={delegated}
          agents={teamCtx.agents()}
          captainName={teamCtx.captainName()}
          userName={viewerName()}
          onAnswer={teamCtx.answer}
          onCancel={teamCtx.cancel}
          onMessage={teamCtx.message}
          onOpenChat={teamCtx.openChat}
          loadThread={teamCtx.loadThread}
          onSummarize={teamCtx.summarize}
        />
      {/if}

      {#if turn.text && !interim}
        {#if isLateReply(turn)}
          <span class="self-start inline-flex items-center rounded-full border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-[10px] leading-none text-amber-700 dark:text-amber-300" data-testid="late-reply-label">{lateLabel(turn)}</span>
        {/if}
        {#if (stamp && !showMeta && !stampOnSpeaker) || isSilentReply || canReply}
          <span class="self-start inline-flex items-center gap-0.5 text-[10px] leading-none text-black-500 dark:text-black-600">
            {#if isSilentReply}
              <svg data-testid="silent-flag" viewBox="0 0 24 24" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-label="Silent — kept out of channels & notifications; shown here only">
                <title>Silent — kept out of channels & notifications; shown here only</title>
                <path d="M8.7 3.7A6 6 0 0 1 18 8.5c0 3 .5 4.4 1.3 5.6M17 17H4c1.5-1.5 3-3 3-8.5"></path>
                <path d="M10.3 21a2 2 0 0 0 3.4 0"></path>
                <line x1="2" y1="2" x2="22" y2="22"></line>
              </svg>
            {/if}
            {#if stamp && !showMeta && !stampOnSpeaker}<span>{stamp}</span>{/if}
            {#if canReply}
            <button
              type="button"
              data-testid="reply-action"
              aria-label="Reply to this message"
              title="Reply"
              onclick={startReply}
              class="inline-flex items-center gap-0.5 rounded px-1 text-[10px] leading-none text-black-500 opacity-0 transition-opacity hover:text-green-700 focus-visible:opacity-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-green-500 group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100 dark:text-black-600 dark:hover:text-green-400"
            >
              <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M6.5 4 2.5 8l4 4M3 8h6.5a4 4 0 0 1 4 4v1" stroke-linecap="round" stroke-linejoin="round"></path></svg>
              Reply
            </button>
          {/if}
          </span>
        {/if}
        <!-- Assistant replies render as plain serif prose (no bubble
             bg/padding/shadow), so the conversation reads like a document —
             only the user's turns wear a bubble. Slightly larger text + roomy
             line-height for comfortable long-form reading. A [silent] reply is
             the same plain text, marked by the muted-bell chip above. -->
        <div use:enrich={displayText} onwick-imagecard-open={onImageCardOpen} class={"wick-prose px-0.5 text-black-900 dark:text-white-100 break-words" + (showMeta ? " wick-prose-sm" : "")}>
          {#each cardSegments as seg}
            {#if seg.kind === "card" && cardMode(seg.card.id, turn.turn_id, cards) !== "none"}
              <ActionCard
                card={seg.card}
                mode={cardMode(seg.card.id, turn.turn_id, cards)}
                clicked={cards[seg.card.id]?.postback}
                onAction={onCardAction}
              />
            {:else}
              <!-- An answer's [from:@handle] tags become muted teammate
                   names (sourceTags.ts); a tag naming no task of this
                   turn is dropped. -->
              {@html turn.role === "assistant"
                ? renderWithSourceTags(seg.kind === "card" ? seg.raw : seg.text, delegated, teamCtx?.agents(), renderMarkdown)
                : renderMarkdown(seg.kind === "card" ? seg.raw : seg.text)}
            {/if}
          {/each}
          {#if turn.interrupted}
            <div class="mt-2 flex items-center gap-1.5 border-t border-white-300 dark:border-navy-600 pt-2">
              {#if isRecovered}
                <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-link-400" fill="none" stroke="currentColor" stroke-width="1.6" data-testid="recovered-icon">
                  <path d="M3 8a5 5 0 109-3M12 2v3H9" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
                <span class="text-xs text-link-400">{interruptedLabel}</span>
                {#if onContinue}<button type="button" data-testid="recovered-continue" onclick={() => onContinue?.()} class="ml-auto shrink-0 rounded-md border border-link-400/60 px-2 py-0.5 text-[11px] font-medium text-link-400 hover:bg-link-400/10">Continue</button>{/if}
              {:else}
              <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-amber-500" fill="none" stroke="currentColor" stroke-width="1.5">
                <path d="M8 2L1.5 13.5h13L8 2z" stroke-linejoin="round"></path>
                <path d="M8 6v4M8 11.5v.5" stroke-linecap="round"></path>
              </svg>
              <span class="text-xs text-amber-600 dark:text-amber-400">{interruptedLabel}</span>
              {/if}
            </div>
          {:else if turn.truncated}
            <p class="mt-2 text-xs text-black-600 dark:text-black-700 italic border-t border-white-300 dark:border-navy-600 pt-2">Output truncated — see raw.jsonl for full content.</p>
          {/if}
        </div>
      {/if}

      {#if delivery}
        <!-- Did the reply really reach Slack? Sent links to the posted message;
             failed says Slack's reason, so a silent drop is never mistaken for
             a delivered answer. -->
        <span
          data-testid="delivery-status"
          data-state={delivery.state}
          class={"self-start inline-flex flex-wrap items-center gap-x-1 gap-y-0.5 text-[11px] leading-4 " +
            (delivery.state === "failed"
              ? "text-red-600 dark:text-red-400"
              : "text-black-500 dark:text-black-600")}
        >
          {#if delivery.state === "sent"}
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-green-600 dark:text-green-400" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M3 8.5 6.5 12 13 4.5" stroke-linecap="round" stroke-linejoin="round"></path>
            </svg>
          {:else if delivery.state === "failed"}
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
              <path d="M8 2L1.5 13.5h13L8 2z" stroke-linejoin="round"></path>
              <path d="M8 6v4M8 11.5v.5" stroke-linecap="round"></path>
            </svg>
          {:else}
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 animate-spin motion-reduce:animate-none" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M8 2a6 6 0 1 1-6 6" stroke-linecap="round"></path>
            </svg>
          {/if}
          {#if delivery.link}
            <!-- The status itself is the link: "Sent to Slack ↗". -->
            <a
              data-testid="delivery-jump"
              href={delivery.link}
              target="_blank"
              rel="noopener noreferrer"
              title="Open the reply's thread in Slack"
              class="inline-flex items-center gap-0.5 break-words rounded hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-green-500"
            >
              {delivery.label}
              <svg viewBox="0 0 16 16" class="h-2.5 w-2.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                <path d="M6 3H3v10h10v-3M9 3h4v4M13 3 7 9" stroke-linecap="round" stroke-linejoin="round"></path>
              </svg>
            </a>
          {:else}
            <span class="break-words">{delivery.label}</span>
          {/if}
          {#if delivery.state === "sent" && (teammatesHidden || agent?.showTeammates === false)}
            <span data-testid="teammates-hidden-note" class="text-black-700 dark:text-black-600" title={TEAMMATES_HIDDEN_TIP}>· {teammatesHiddenNote(delivery.label)}</span>
          {/if}
        </span>
      {/if}

      {#if onRemoteRecheck && turn.role === "assistant" && turn.remote_note === NOTE_NO_MARKER}
        <RemoteRecheck onRecheck={onRemoteRecheck} shown={turn.text} />
      {/if}

      {#if safeArtifacts.length > 0}
        <ArtifactGallery artifacts={safeArtifacts} onOpen={openLightbox} />
      {/if}

      {#if !turn.text && turn.interrupted}
        <div class="rounded-2xl rounded-tl-sm border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-4 py-3 shadow-sm">
          <div class="flex items-center gap-1.5">
            {#if isRecovered}
              <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-link-400" fill="none" stroke="currentColor" stroke-width="1.6" data-testid="recovered-icon">
                <path d="M3 8a5 5 0 109-3M12 2v3H9" stroke-linecap="round" stroke-linejoin="round"></path>
              </svg>
              <span class="text-xs text-link-400">{interruptedLabel}</span>
              {#if onContinue}<button type="button" data-testid="recovered-continue" onclick={() => onContinue?.()} class="ml-auto shrink-0 rounded-md border border-link-400/60 px-2 py-0.5 text-[11px] font-medium text-link-400 hover:bg-link-400/10">Continue</button>{/if}
            {:else}
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-amber-500" fill="none" stroke="currentColor" stroke-width="1.5">
              <path d="M8 2L1.5 13.5h13L8 2z" stroke-linejoin="round"></path>
              <path d="M8 6v4M8 11.5v.5" stroke-linecap="round"></path>
            </svg>
            <span class="text-xs text-amber-700 dark:text-amber-300">{interruptedLabel}</span>
            {/if}
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}

<MediaLightbox items={lightbox?.items ?? null} index={lightbox?.index ?? 0} onClose={closeLightbox} />

<style>
  /* transform + opacity only: no layout work, so the bottom pin is unaffected. */
  .wick-enter-up {
    animation: wick-enter-up 180ms ease-out both;
  }
  @keyframes wick-enter-up {
    from { opacity: 0; transform: translateY(8px); }
    to { opacity: 1; transform: none; }
  }
  @media (prefers-reduced-motion: reduce) {
    .wick-enter-up { animation: none; }
  }
</style>
