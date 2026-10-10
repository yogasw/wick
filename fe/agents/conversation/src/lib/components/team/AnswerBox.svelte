<script lang="ts">
  import TeamIcon from "./TeamIcon.svelte";

  /* The answer box of the Team blocks (mockup .dans): an optional row of
     quick pills above it, then one bordered box whose header line says
     where the answer goes ("Answer goes to @franky · Captain sees it
     too") and whose input row ends in a 28px send button. Shared by the
     needs-input section, Reply in thread, the tray and the Tasks tab.
     warn is the amber question box, ok the green reply one. Enter sends
     (it is a form); an empty box greys the button out, and while the
     answer is on its way everything stays disabled and the button spins.
     A quick pill sends its text straight away. */
  type Props = {
    /** The teammate's handle, without the @. */
    to: string;
    /** After "@to · " in the header; no note, no separator. */
    note?: string;
    /** The box never clears itself after a send: the caller owns the text,
        through bind:value or onInput, and empties it once the send lands. */
    value?: string;
    placeholder?: string;
    /** The input's accessible name; "Answer @to" by default. */
    label?: string;
    /** The longest text the input takes, in characters (none by default). */
    maxlength?: number;
    quick?: string[];
    sending?: boolean;
    /** Sending is blocked (another answer is on its way); typing is not. */
    disabled?: boolean;
    tone?: "warn" | "ok";
    onSend: (text: string) => void;
    onInput?: (text: string) => void;
  };
  let {
    to,
    note,
    value = $bindable(""),
    placeholder = "Type an answer…",
    label,
    maxlength,
    quick = [],
    sending = false,
    disabled = false,
    tone = "warn",
    onSend,
    onInput,
  }: Props = $props();

  const off = $derived(!value.trim());
  const locked = $derived(sending || disabled);

  function send(text: string) {
    if (locked || !text.trim()) return;
    onSend(text.trim());
  }
</script>

<div data-testid="answer-box" data-tone={tone} class="flex flex-col gap-2">
  {#if quick.length > 0}
    <div class="flex flex-wrap gap-1.5">
      {#each quick as o}
        <button
          type="button"
          data-testid="quick-pill"
          disabled={locked}
          onclick={() => send(o)}
          class="rounded-full border border-white-300 bg-white-100 px-[9px] py-0.5 text-[11px] text-black-800 hover:border-amber-500/[.45] hover:text-amber-700 focus:outline-none focus-visible:border-amber-500/[.45] focus-visible:text-amber-700 disabled:cursor-not-allowed disabled:opacity-50 dark:border-navy-600 dark:bg-navy-800 dark:text-black-600 dark:hover:border-[rgba(251,191,36,.40)] dark:hover:text-amber-300 dark:focus-visible:border-[rgba(251,191,36,.40)] dark:focus-visible:text-amber-300"
        >{o}</button>
      {/each}
    </div>
  {/if}
  <form
    class={["overflow-hidden rounded-[10px] border bg-white-100 dark:bg-navy-800",
      tone === "warn" ? "border-amber-500/[.45] focus-within:border-amber-500 dark:border-[rgba(251,191,36,.40)] dark:focus-within:border-amber-300" : "border-white-300 focus-within:border-green-500 dark:border-navy-600"]}
    onsubmit={(e) => { e.preventDefault(); send(value); }}
  >
    <div data-testid="answer-to" class="flex items-center gap-1.5 border-b border-white-300 bg-white-200 px-2.5 py-1 text-[10.5px] text-black-700 dark:border-navy-600 dark:bg-navy-700">
      <TeamIcon name="reply" size={11} /><span class="min-w-0 truncate">Answer goes to <b class={["font-semibold", tone === "warn" ? "text-amber-700 dark:text-amber-300" : "text-green-600 dark:text-green-400"]}>@{to}</b>{note ? " · " + note : ""}</span>
    </div>
    <div class="flex items-center gap-2 py-1.5 pl-2.5 pr-1.5">
      <input
        type="text"
        aria-label={label ?? "Answer @" + to}
        {placeholder}
        {maxlength}
        {value}
        oninput={(e) => { value = (e.currentTarget as HTMLInputElement).value; onInput?.(value); }}
        class="min-w-0 flex-1 bg-transparent py-1 text-[12.5px] text-black-900 placeholder:text-black-700 focus:outline-none disabled:cursor-not-allowed dark:text-white-100"
      />
      <button
        type="submit"
        aria-label="Send answer"
        data-off={off}
        data-sending={sending}
        disabled={locked || off}
        class={["grid h-7 w-7 shrink-0 place-items-center rounded focus:outline-none focus-visible:ring-1 focus-visible:ring-green-500 disabled:cursor-not-allowed",
          off && !sending ? "bg-white-300 text-black-700 dark:bg-navy-600" : "bg-green-500 text-white-100 hover:bg-green-600"]}
      >
        {#if sending}<TeamIcon name="spin" size={14} spin />{:else}<TeamIcon name="send" size={14} />{/if}
      </button>
    </div>
  </form>
</div>
