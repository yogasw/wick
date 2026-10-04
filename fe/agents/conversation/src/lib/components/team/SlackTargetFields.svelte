<script lang="ts">
  /* Where a Slack remote agent's turns go: a DM, a channel (each chat opens
     a thread there) or one existing thread. Shared by the + Agent wizard
     and Settings. Users, bots and channels are picked by searching the
     workspace (ids can still be typed); a pasted message link fills
     channel and thread. */
  import type { SlackIdentity, SlackTarget } from "../../api/team.js";
  import { TARGET_OPTIONS, parseSlackLink } from "../../slackRemote.js";
  import SlackDirectoryPicker from "./SlackDirectoryPicker.svelte";

  type Props = {
    target: SlackTarget;
    channel: string;
    user: string;
    mentionId: string;
    threadTs: string;
    targetName: string;
    idPrefix: string;
    /* The workspace searched; without a connector only typing ids works. */
    base?: string;
    connectorId?: string;
    identity?: SlackIdentity;
    accountId?: string;
  };
  let {
    target = $bindable(), channel = $bindable(), user = $bindable(), mentionId = $bindable(),
    threadTs = $bindable(), targetName = $bindable(), idPrefix,
    base = "", connectorId = "", identity = "bot", accountId = "",
  }: Props = $props();

  let link = $state("");
  let linkError = $state("");

  function pasteLink(v: string) {
    link = v;
    linkError = "";
    if (!v.trim()) return;
    const r = parseSlackLink(v);
    if (!r) {
      linkError = "Not a Slack message link (…/archives/C…/p…).";
      return;
    }
    channel = r.channel;
    if (target === "thread") threadTs = r.thread_ts;
  }

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const hint = "mt-1 text-xs text-black-800 dark:text-black-600";
</script>

<div class="space-y-3">
  <div>
    <p class={label} id="{idPrefix}-target-label">Send to</p>
    <div class="inline-flex rounded-lg border border-white-300 p-0.5 dark:border-navy-600" role="group" aria-labelledby="{idPrefix}-target-label">
      {#each TARGET_OPTIONS as o (o.value)}
        <button
          type="button"
          class="rounded-md px-3 py-1 text-xs {target === o.value ? 'bg-green-500 text-white-100' : 'text-black-800 dark:text-black-600'}"
          aria-pressed={target === o.value}
          onclick={() => (target = o.value)}
        >{o.label}</button>
      {/each}
    </div>
    <p class={hint}>{TARGET_OPTIONS.find((o) => o.value === target)?.hint}</p>
  </div>

  {#if target === "dm"}
    <SlackDirectoryPicker
      {base} {connectorId} {identity} {accountId} kind="users" bind:value={user} bind:name={targetName}
      inputId="{idPrefix}-user" idLabel="User or bot ID" idPlaceholder="U0123ABCD" idHint="From the Slack profile: ⋮ › Copy member ID."
    />
  {:else}
    {#if target === "thread"}
      <div>
        <label class={label} for="{idPrefix}-link">Thread link</label>
        <input id="{idPrefix}-link" class="{input} font-mono" value={link} oninput={(e) => pasteLink((e.currentTarget as HTMLInputElement).value)} placeholder="https://acme.slack.com/archives/C0123/p1700000000123456" />
        {#if linkError}<p class="mt-1 text-xs text-neg-400">{linkError}</p>{:else}<p class={hint}>Paste the message link; channel and thread fill in.</p>{/if}
      </div>
    {/if}
    <SlackDirectoryPicker
      {base} {connectorId} {identity} {accountId} kind="channels" bind:value={channel} bind:name={targetName}
      inputId="{idPrefix}-channel" idLabel="Channel ID" idPlaceholder="C0123ABCD"
    />
    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {#if target === "thread"}
        <div>
          <label class={label} for="{idPrefix}-thread">Thread timestamp</label>
          <input id="{idPrefix}-thread" class="{input} font-mono" bind:value={threadTs} placeholder="1700000000.123456" />
        </div>
      {:else}
        <div>
          <label class={label} for="{idPrefix}-mention">Mention (optional)</label>
          <input id="{idPrefix}-mention" class="{input} font-mono" bind:value={mentionId} placeholder="U0123ABCD" />
        </div>
      {/if}
    </div>
    {#if target === "channel"}<p class={hint}>The user or bot @-mentioned in each message (see "Always @mention the target").</p>{/if}
  {/if}

  <div>
    <label class={label} for="{idPrefix}-name">Display name</label>
    <input id="{idPrefix}-name" class={input} bind:value={targetName} placeholder={target === "dm" ? "@helper" : "#ops"} />
    <p class={hint}>How wick names the target, e.g. in the chat caption.</p>
  </div>
</div>
