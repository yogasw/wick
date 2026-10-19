<script lang="ts">
  /* 👂 Listening / Off chip for a session bound to a Slack channel thread,
     with the toggle and this thread's instructions behind a click. Renders
     nothing for any other session (the API answers 404). */
  import { untrack } from "svelte";
  import { runApi } from "../api/team.js";
  import { getListen, setListen, delayText, type ListenState } from "../api/listen.js";

  type Props = { base: string; sessionId: string };
  let { base, sessionId }: Props = $props();

  let st = $state<ListenState | null>(null);
  let open = $state(false);
  let draft = $state("");
  let busy = $state(false);
  let err = $state("");
  let btn: HTMLButtonElement | undefined = $state();
  // Fixed, not absolute: the Team header line clips what overflows it.
  let pos = $state({ top: 0, left: 0 });

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

  function toggleOpen() {
    open = !open;
    if (open && btn) {
      const r = btn.getBoundingClientRect();
      pos = { top: r.bottom + 4, left: Math.max(8, Math.min(r.left, window.innerWidth - 328)) };
    }
    if (open && st) draft = st.thread_prompt ?? "";
    err = "";
  }

  async function apply(action: "on" | "off") {
    busy = true;
    err = "";
    try {
      st = await runApi(setListen(base, sessionId, action, action === "on" ? draft : undefined));
      if (action === "off") open = false;
    } catch (e) {
      err = String((e as { message?: string })?.message ?? e);
    } finally {
      busy = false;
    }
  }

  const chipClass = $derived(
    st?.listening
      ? "border-teal-500 bg-teal-100 text-teal-600 dark:bg-teal-800/30 dark:text-teal-500"
      : "border-white-300 text-black-700 dark:border-navy-600 dark:text-black-600",
  );
</script>

{#if st}
  <span class="relative inline-flex" data-testid="listen-chip" data-listening={st.listening}>
    <button
      bind:this={btn}
      type="button"
      class="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium {chipClass}"
      title={st.listening ? `Listening to this Slack thread · delay ${delayText(st.delay_sec)} · ${st.scope} · ${st.on_team_reply}` : "Not listening to this Slack thread"}
      onclick={toggleOpen}
    >👂 {st.listening ? "Listening" : "Off"}</button>
    {#if open}
      <div style="top: {pos.top}px; left: {pos.left}px" class="fixed z-50 w-80 space-y-2 rounded-xl border border-white-300 bg-white-100 p-3 text-xs text-black-900 shadow-lg dark:border-navy-600 dark:bg-navy-800 dark:text-white-100" data-testid="listen-panel">
        <p class="font-medium">{st.listening ? "Listening to this thread" : "Not listening"}</p>
        <p class="text-black-800 dark:text-black-600">Delay {delayText(st.delay_sec)} · scope {st.scope} · when someone else answers: {st.on_team_reply}</p>
        <label class="block font-medium" for="listen-draft-{sessionId}">This thread only</label>
        <textarea id="listen-draft-{sessionId}" rows="3" class="w-full rounded-lg border border-white-300 bg-white-100 px-2 py-1 dark:border-navy-600 dark:bg-navy-700" placeholder="e.g. only WABA questions" bind:value={draft}></textarea>
        {#if st.instructions}
          <details>
            <summary class="cursor-pointer text-black-800 dark:text-black-600">Instructions in effect</summary>
            <p class="mt-1 max-h-40 overflow-y-auto whitespace-pre-line text-black-800 dark:text-black-600" data-testid="listen-instructions">{st.instructions}</p>
          </details>
        {/if}
        {#if err}<p class="text-neg-400">{err}</p>{/if}
        <div class="flex justify-end gap-2">
          {#if st.listening}
            <button type="button" class="rounded-lg border border-white-300 px-2 py-1 dark:border-navy-600" disabled={busy} onclick={() => apply("off")}>Stop listening</button>
            <button type="button" class="rounded-lg bg-teal-600 px-2 py-1 text-white-100" disabled={busy} onclick={() => apply("on")}>Save</button>
          {:else}
            <button type="button" class="rounded-lg bg-teal-600 px-2 py-1 text-white-100" disabled={busy} onclick={() => apply("on")}>Start listening</button>
          {/if}
        </div>
      </div>
    {/if}
  </span>
{/if}
