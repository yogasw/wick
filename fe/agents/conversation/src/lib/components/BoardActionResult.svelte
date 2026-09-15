<script lang="ts">
  /* What a ticket-list button answered, and what it is still doing.

     A toast was enough while a button meant "POST one ticket and tell me the
     HTTP status". It is not enough for a button that starts a job: the work
     outlives the request, so the interesting part — how far it got, what it
     created, what it refused — arrives AFTER the toast would have faded.

     So a board action gets a small floating card in the bottom-right corner
     instead: a toast that can keep talking. It follows the run, tells the
     board to refresh as counters move, and then gets out of the way on its
     own — a finished run closes itself after a visible countdown, because
     nobody dismisses a box that is only telling them something worked.

     It does NOT take a slice of the board. The first version sat above the
     columns and pushed them down by a third of the screen, most of it an
     empty frame around HTML that repeated what the card already said. */
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
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
  /* Seconds left before a finished card closes itself; -1 = not counting. */
  let closeIn = $state(-1);
  let hovering = $state(false);
  /* The receiver's HTML is opt-in to LOOK at, always: it is somebody else's
     markup of unknown size, and the card is 20rem wide. */
  let showHtml = $state(false);

  /* How long a finished card lingers. Long enough to read the counters,
     short enough that nobody has to close it. */
  const CLOSE_AFTER = 8;

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

  /* Auto-close, but only for an outcome nobody needs to act on. A failure
     stays until it is dismissed: it is the one result somebody has to see,
     and a box that vanishes is a box that was never read.

     Pauses while the pointer is on the card — reading it should not be a
     race against it. */
  $effect(() => {
    if (state === "running" || state === "error") {
      closeIn = -1;
      return;
    }
    closeIn = CLOSE_AFTER;
    const t = setInterval(() => {
      if (hovering) return;
      closeIn -= 1;
      if (closeIn <= 0) {
        clearInterval(t);
        onDismiss?.();
      }
    }, 1000);
    return () => clearInterval(t);
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

<!-- Bottom-right, above the composer, never in the board's column space.
     Pointer-events on the card only, so the strip of screen it occupies is
     still clickable where it is transparent. -->
<div
  data-testid="board-action-result"
  role="status"
  onmouseenter={() => { hovering = true; }}
  onmouseleave={() => { hovering = false; }}
  class="fixed bottom-24 right-4 z-40 w-[22rem] max-w-[calc(100vw-2rem)] rounded-xl border border-white-300 bg-white-100 p-2.5 shadow-lg dark:border-navy-600 dark:bg-navy-700"
>
  <div class="flex items-start gap-2">
    <span
      class={"mt-0.5 shrink-0 rounded-full border px-1.5 py-0.5 text-[10px] font-semibold " + (TONE[state] ?? TONE.done)}
    >
      {#if state === "running"}
        <span class="mr-1 inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-link-400 align-middle"></span>
      {/if}
      {WORD[state] ?? state}
    </span>
    <div class="min-w-0 flex-1">
      <p class="truncate text-[11px] font-medium text-black-900 dark:text-white-100">{label}</p>
      <p class="mt-0.5 line-clamp-3 text-[11px] leading-snug text-black-700 dark:text-black-600">{text}</p>
    </div>
    <button
      type="button"
      aria-label="Dismiss result"
      title={closeIn > 0 ? `Closing in ${closeIn}s` : "Dismiss"}
      onclick={() => onDismiss?.()}
      class="shrink-0 rounded-lg px-1.5 text-xs text-black-700 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600"
    >×</button>
  </div>

  {#if pct >= 0}
    <div class="mt-2 flex items-center gap-2">
      <div class="h-1 flex-1 overflow-hidden rounded-full bg-white-300 dark:bg-navy-800">
        <div
          class={"h-full rounded-full transition-[width] duration-300 " +
            (state === "error" ? "bg-neg-400" : "bg-green-500")}
          style={`width:${pct}%`}
        ></div>
      </div>
      <span class="shrink-0 font-mono text-[10px] text-black-600 dark:text-black-700">
        {live.progress?.done ?? 0}/{live.progress?.total ?? 0}
      </span>
    </div>
  {/if}

  {#if counts.length > 0}
    <div class="mt-1.5 flex flex-wrap gap-1">
      {#each counts as [k, v] (k)}
        <span
          class="rounded-full bg-white-200 px-1.5 py-0.5 text-[10px] text-black-800 dark:bg-navy-800 dark:text-black-600"
        >{k} <span class="font-mono font-semibold">{v}</span></span>
      {/each}
    </div>
  {/if}

  <!-- Only when a receiver actually sent markup, and only when asked for:
       it is somebody else's HTML of unknown size, and this card is 22rem
       wide. A bare sandboxed frame — no scripts, no network, and none of
       the artifact chrome (Full screen / Show code / Download), which
       belongs to files somebody wants to keep, not to a status line. -->
  {#if (live.html ?? "").trim() !== ""}
    <button
      type="button"
      onclick={() => { showHtml = !showHtml; }}
      class="mt-1.5 text-[10px] text-link-400 hover:underline"
    >{showHtml ? "Hide details" : "Details"}</button>
    {#if showHtml}
      <iframe
        title={`${label} details`}
        sandbox=""
        srcdoc={live.html}
        class="mt-1 h-40 w-full rounded-lg border border-white-300 bg-white-200 dark:border-navy-600 dark:bg-navy-800"
      ></iframe>
    {/if}
  {/if}

  <div class="mt-1.5 flex items-center justify-between text-[10px] text-black-600 dark:text-black-700">
    <span>
      {#if polling}
        following the run · {polls} check{polls === 1 ? "" : "s"}
      {/if}
    </span>
    {#if closeIn > 0}
      <span>{hovering ? "paused" : `closing in ${closeIn}s`}</span>
    {/if}
  </div>
</div>
