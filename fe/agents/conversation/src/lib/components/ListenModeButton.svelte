<script lang="ts">
  /* The composer toolbar's reply-mode button for a session bound to a Slack
     channel thread: a lightning icon, a teal dot while the thread listens
     or auto-replies, the details on hover and the Auto-reply / Listen /
     Manual switch on click. Renders nothing for any other session (the
     API answers 404). */
  import { untrack } from "svelte";
  import { runApi } from "../api/team.js";
  import { getListen, setReplyMode, delayText, type ListenState, type ReplyMode } from "../api/listen.js";
  import HoverCard from "./HoverCard.svelte";

  type Props = { base: string; sessionId: string };
  let { base, sessionId }: Props = $props();

  let st = $state<ListenState | null>(null);
  let hover = $state(false);
  let open = $state(false);
  let busy = $state(false);
  let err = $state("");
  let btn: HTMLButtonElement | undefined = $state();
  let wrap: HTMLSpanElement | undefined = $state();

  function load(sid: string) {
    runApi(getListen(base, sid))
      .then((r) => { if (sid === sessionId) st = r; })
      .catch(() => { if (sid === sessionId) st = null; });
  }

  $effect(() => {
    const sid = sessionId;
    st = null;
    open = false;
    if (!sid) return;
    untrack(() => load(sid));
    // A 🤖 or a "stop" in Slack changes it without this page: re-read now and then.
    const t = setInterval(() => load(sid), 30_000);
    return () => clearInterval(t);
  });

  $effect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => { if (wrap && !wrap.contains(e.target as Node)) open = false; };
    const esc = (e: KeyboardEvent) => { if (e.key === "Escape") open = false; };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", esc);
    return () => { document.removeEventListener("mousedown", close); document.removeEventListener("keydown", esc); };
  });

  const MODES: { mode: ReplyMode; label: string; desc: string }[] = [
    { mode: "auto_reply", label: "Auto-reply", desc: "Answers every reply, no mention needed" },
    { mode: "listen", label: "Listen", desc: "Reads every reply, answers when it is meant for it" },
    { mode: "manual", label: "Manual", desc: "Answers only when mentioned" },
  ];

  const mode = $derived<ReplyMode>(st?.mode ?? (st?.listening ? "listen" : "manual"));
  const channel = $derived(st?.channel_name ? "#" + st.channel_name : "this channel");
  const card = $derived.by(() => {
    const m = MODES.find((x) => x.mode === mode)!;
    const rows: [string, string][] = [["Means", m.desc], ["Channel", "Slack · " + channel]];
    if (mode !== "manual") rows.push(["Holds messages", st?.hold_sec ? `${delayText(st.hold_sec)}, waiting for follow-ups` : "No, passed on at once"]);
    return { title: mode === "manual" ? "Manual in this thread" : `${m.label} is on in this thread`, rows };
  });

  async function choose(m: ReplyMode) {
    if (busy) return;
    busy = true;
    err = "";
    try {
      st = await runApi(setReplyMode(base, sessionId, m));
      open = false;
    } catch (e) {
      err = String((e as { message?: string })?.message ?? e);
    } finally {
      busy = false;
    }
  }

  // Which of the three changes the channel's rule rather than this thread.
  const touchesChannel = (m: ReplyMode) =>
    m !== "manual" && ((st?.channel_mode === "auto_reply") !== (m === "auto_reply"));
</script>

{#if st}
  <span bind:this={wrap} class="relative inline-flex shrink-0" data-testid="listen-mode" data-mode={mode}>
    <button
      bind:this={btn}
      type="button"
      aria-label="Reply mode"
      aria-haspopup="menu"
      aria-expanded={open}
      onmouseenter={() => (hover = true)}
      onmouseleave={() => (hover = false)}
      onfocus={() => (hover = true)}
      onblur={() => (hover = false)}
      onclick={() => { open = !open; hover = false; err = ""; }}
      class={"relative inline-flex items-center justify-center h-8 w-8 shrink-0 rounded-lg border bg-white-100 dark:bg-navy-700 hover:bg-white-200 dark:hover:bg-navy-600 transition-colors " +
        (mode === "manual" ? "border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600" : "border-teal-500/50 text-teal-600 dark:text-teal-500")}
    >
      <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" aria-hidden="true">
        <path d="M8.75 1.75 3.25 9.25h4.5l-.5 5 5.5-7.5h-4.5l.5-5z"></path>
      </svg>
      {#if mode !== "manual"}
        <span data-testid="listen-mode-dot" class="absolute -top-0.5 -right-0.5 h-2 w-2 rounded-full bg-teal-500 ring-2 ring-white-100 dark:ring-navy-700" aria-hidden="true"></span>
      {/if}
    </button>
    {#if hover && !open}
      <HoverCard anchor={btn} title={card.title} rows={card.rows} foot="Click to change" testid="listen-mode-card" />
    {/if}
    {#if open}
      <div role="menu" data-testid="listen-mode-menu" class="absolute bottom-full left-0 z-50 mb-2 w-72 rounded-xl border border-white-300 bg-white-100 p-1.5 text-xs shadow-lg dark:border-navy-600 dark:bg-navy-700">
        <p class="px-2 pb-1 pt-1.5 text-[11px] uppercase tracking-wide text-black-700 dark:text-black-600">Replies in {channel}</p>
        {#each MODES as m (m.mode)}
          <button
            type="button"
            role="menuitemradio"
            aria-checked={m.mode === mode}
            disabled={busy}
            onclick={() => choose(m.mode)}
            class="flex w-full items-start gap-2 rounded-lg px-2 py-1.5 text-left text-black-900 hover:bg-white-200 disabled:opacity-60 dark:text-white-100 dark:hover:bg-navy-600"
          >
            <span class="w-3 shrink-0 text-teal-600 dark:text-teal-500">{m.mode === mode ? "✓" : ""}</span>
            <span class="min-w-0">
              <span class="block font-medium">{m.label}</span>
              <span class="block text-[11px] text-black-700 dark:text-black-600">{m.desc}{m.mode === "manual" ? " · this thread" : touchesChannel(m.mode) ? " · whole channel" : ""}</span>
            </span>
          </button>
        {/each}
        {#if err}<p class="px-2 py-1 text-neg-400">{err}</p>{/if}
      </div>
    {/if}
  </span>
{/if}
