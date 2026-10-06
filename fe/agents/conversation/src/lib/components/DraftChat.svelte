<script lang="ts">
  /* A draft chat: "+ New chat" opens this instead of creating a session,
     like the classic New session page. The chat is created on the first
     Send; leaving the draft unsent leaves no empty chat behind. */
  import { Composer } from "@wick-fe/common-ui";
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import type { AgentItem, SessionField } from "../api/team.js";
  import type { DraftMessage } from "../agentChats.js";
  import SessionFieldChips from "./SessionFieldChips.svelte";

  type Props = {
    agent: AgentItem;
    /** Creates the chat and sends the first message, with the session
        fields' answers (empty = the plugin's defaults). */
    onSend: (msg: DraftMessage, options: Record<string, string>) => Promise<void>;
    /** Loads the new-session fields a plugin remote agent declares; unset
        or empty = none (no chips). */
    loadFields?: () => Promise<SessionField[]>;
  };
  let { agent, onSend, loadFields }: Props = $props();

  let sending = $state(false);
  let fields = $state<SessionField[]>([]);
  let options = $state<Record<string, string>>({});

  $effect(() => {
    const load = loadFields;
    fields = [];
    options = {};
    if (!load) return;
    let live = true;
    load()
      .then((f) => {
        if (live) fields = f ?? [];
      })
      .catch(() => {
        // No chips when the plugin cannot be asked; the defaults still apply.
      });
    return () => {
      live = false;
    };
  });

  async function send(msg: DraftMessage) {
    if (sending || (!msg.text.trim() && msg.files.length === 0)) return;
    sending = true;
    try {
      await onSend(msg, fields.length > 0 ? options : {});
    } finally {
      sending = false;
    }
  }
</script>

<div class="mx-auto flex h-full w-full max-w-2xl flex-col items-center justify-center px-6" data-draft-chat>
  <div class="mb-6 flex flex-col items-center text-center">
    {#if agent.avatar}
      <AgentAvatar kind={agent.avatar.kind} shape={agent.avatar.shape} expression={agent.avatar.expression} color={agent.avatar.color} size={48} live />
    {/if}
    <h1 class="mt-3 text-xl font-semibold text-black-900 dark:text-white-100">New chat with {agent.name}</h1>
    <p class="mt-1 text-sm text-black-800 dark:text-black-600">Draft — the chat is created when you send the first message.</p>
  </div>
  <div class="w-full">
    {#if fields.length > 0}
      <div class="mb-2"><SessionFieldChips {fields} values={options} onChange={(v) => (options = v)} /></div>
    {/if}
    <Composer onSend={send} disabled={sending} placeholder={`Message @${agent.handle}…`} />
  </div>
</div>
