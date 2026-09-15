<script lang="ts">
  /* What a ticket-list button answered, and what it is still doing.

     A toast was enough while a button meant "POST one ticket and tell me the
     HTTP status". It is not enough for a button that starts a job: the work
     outlives the request, so the interesting part — how far it got, what it
     created, what it refused — arrives AFTER the toast would have faded.

     So a board action gets a panel instead. It draws whatever the receiver
     sent back (see ActionPayload), follows the run while it says it is
     running, and tells the board to refresh when it stops. */
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import HtmlArtifact from "./HtmlArtifact.svelte";
  import { pollBoardAction, type ActionPayload, type ActionResult } from "../api/tickets.js";

  type Props = {
    base: string;
    projectId: string;
    buttonId: string;
    label: string;
    /** The click's answer. Replacing it restarts the follow. */
    result: ActionResult;
    /** The run wrote something: at the end, and every time its counters
        move while it is still going. The board re-reads on this, so cards
        land in their new column as the sync walks the list instead of after
        somebody presses reload. */
    onFinished?: () => void;
    onDismiss?: () => void;
  };

  let { base, projectId, buttonId, label, result, onFinished, onDismiss }: Props = $props();

  /* The newest payload: the click's own, then whatever polling returns. */
  let live = $state<ActionPayload>({});
  let transport = $state<{ ok: boolean; error?: string; status?: number }>({ ok: true });
  let polls = $state(0);
  let polling = $state(false);

  /* A receiver that answers every 3s for 10 minutes is either wedged or
     talking to nobody; either way the panel stops asking and says so. */
  const MAX_POLLS = 200;
  const POLL_MS = 3000;

  const RUNNING = ["running", "queued", "busy", "in_progress", "started"];
  const BAD = ["error", "failed", "refused"];

  const state = $derived.by(() => {
    if (!transport.ok) return "error";
    const s = (live.status ?? "").toLowerCase();
    if (BAD.includes(s)) return "error";
    if (RUNNING.includes(s)) return "running";
    if (s === "ignored" || s === "skipped") return "ignored";
    return "done";
  });

  const text = $derived(
    live.message ||
      (transport.ok ? "" : transport.error || `HTTP ${transport.status ?? 0}`) ||
      "No message from the receiver.",
  );

  const pct = $derived.by(() => {
    const total = live.progress?.total ?? 0;
    const done = live.progress?.done ?? 0;
    if (total <= 0) return -1;
    return Math.max(0, Math.min(100, Math.round((done / total) * 100)));
  });

  const counts = $derived(Object.entries(live.counts ?? {}));

  /* One follow per result object. $effect re-runs when the prop is replaced,
     which is exactly when a new click happened — the old loop sees the
     generation change and stops.

     Everything the effect DECIDES with comes from the prop, never from the
     state it just wrote: an effect that reads its own writes re-runs itself
     forever (Svelte stops it with effect_update_depth_exceeded, which is
     what the first version of this did). */
  let generation = 0;
  $effect(() => {
    const first = result.result ?? {};
    const mine = ++generation;
    live = first;
    transport = { ok: result.ok, error: result.error, status: result.status };
    polls = 0;

    const url = first.poll_url;
    const shouldFollow =
      result.ok && !!url && RUNNING.includes((first.status ?? "").toLowerCase());
    if (!shouldFollow) {
      /* A click that finished the work outright still leaves the board a
         version behind. */
      if (result.ok) onFinished?.();
      return;
    }

    polling = true;
    let timer: ReturnType<typeof setTimeout> | null = null;
    /* How far the run had got at the last board refresh. */
    let lastMoved = -1;

    const tick = () => {
      if (mine !== generation) return;
      Effect.runPromise(
        pollBoardAction(base, projectId, buttonId, url!).pipe(Effect.provide(WickClientLayer)),
      )
        .then((r) => {
          if (mine !== generation) return;
          const now = r.result ?? {};
          transport = { ok: r.ok, error: r.error, status: r.status };
          if (r.result) live = now;
          const n = polls + 1;
          polls = n;
          const s = (now.status ?? "").toLowerCase();
          /* Refresh the board as the run moves, not only when it ends: a
             sync over a few dozen tickets takes minutes, and watching the
             cards travel is the difference between "it is working" and
             "did I break it". Only on an actual change, so a quiet poll
             costs nothing. */
          const moved = (now.progress?.done ?? 0) + (now.counts ? Object.keys(now.counts).length : 0);
          if (moved !== lastMoved) {
            lastMoved = moved;
            onFinished?.();
          }
          if (r.ok && RUNNING.includes(s) && n < MAX_POLLS) {
            timer = setTimeout(tick, POLL_MS);
            return;
          }
          polling = false;
          /* Whatever the verdict, the run touched tickets — the board is
             showing yesterday's copy until it re-reads. */
          onFinished?.();
        })
        .catch((e: unknown) => {
          if (mine !== generation) return;
          polling = false;
          transport = { ok: false, error: e instanceof Error ? e.message : "poll failed" };
        });
    };

    timer = setTimeout(tick, POLL_MS);
    return () => {
      polling = false;
      if (timer) clearTimeout(timer);
    };
  });

  const TONE: Record<string, string> = {
    running: "border-link-300 bg-link-100 text-link-400",
    done: "border-pos-300 bg-pos-100 text-pos-400",
    ignored: "border-cau-300 bg-cau-100 text-cau-400",
    error: "border-neg-300 bg-neg-100 text-neg-400",
  };
  const WORD: Record<string, string> = {
    running: "Running",
    done: "Done",
    ignored: "Nothing to do",
    error: "Failed",
  };
</script>

<div
  data-testid="board-action-result"
  class="rounded-xl border border-white-300 bg-white-100 p-3 dark:border-navy-600 dark:bg-navy-700"
>
  <div class="flex items-start gap-2">
    <span
      class={"shrink-0 rounded-full border px-2 py-0.5 text-[11px] font-semibold " + (TONE[state] ?? TONE.done)}
    >
      {#if state === "running"}
        <span class="mr-1 inline-block h-2 w-2 animate-pulse rounded-full bg-link-400 align-middle"></span>
      {/if}
      {WORD[state] ?? state}
    </span>
    <div class="min-w-0 flex-1">
      <p class="truncate text-xs font-medium text-black-900 dark:text-white-100">{label}</p>
      <p class="mt-0.5 text-[11px] leading-relaxed text-black-700 dark:text-black-600">{text}</p>
    </div>
    <button
      type="button"
      aria-label="Dismiss result"
      onclick={() => onDismiss?.()}
      class="shrink-0 rounded-lg px-1.5 text-black-700 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600"
    >×</button>
  </div>

  {#if pct >= 0}
    <div class="mt-2.5">
      <div class="h-1.5 w-full overflow-hidden rounded-full bg-white-300 dark:bg-navy-800">
        <div
          class={"h-full rounded-full transition-[width] duration-300 " +
            (state === "error" ? "bg-neg-400" : "bg-green-500")}
          style={`width:${pct}%`}
        ></div>
      </div>
      <p class="mt-1 text-right text-[10px] font-mono text-black-600 dark:text-black-700">
        {live.progress?.done ?? 0}/{live.progress?.total ?? 0}
      </p>
    </div>
  {/if}

  {#if counts.length > 0}
    <div class="mt-2 flex flex-wrap gap-1.5">
      {#each counts as [k, v] (k)}
        <span
          class="rounded-full bg-white-200 px-2 py-0.5 text-[10px] font-medium text-black-800 dark:bg-navy-800 dark:text-black-600"
        >{k} <span class="font-mono font-semibold">{v}</span></span>
      {/each}
    </div>
  {/if}

  {#if live.html}
    <!-- The receiver's own rendering, in the same sandbox an HTML artifact
         gets: it may script and style itself, it may not reach the network
         or this page. -->
    <div class="mt-2.5">
      <HtmlArtifact src={live.html} name={`${label}.html`} />
    </div>
  {/if}

  {#if polling}
    <p class="mt-2 text-[10px] text-black-600 dark:text-black-700">
      following the run — checked {polls} time{polls === 1 ? "" : "s"}
    </p>
  {/if}
</div>
