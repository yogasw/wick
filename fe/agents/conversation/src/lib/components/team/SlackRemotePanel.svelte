<script lang="ts">
  /* Settings of a Slack remote agent, two halves of one drawer: section
     "remote" is the Remote tab (target, identity, listen, marker, @mention, idle/max,
     Test) and "advanced" who may use it. A target is several fields that
     only make sense together, so the Remote tab saves with one button;
     Usage saves on its own like the rest of Settings. Both go through
     PATCH …/slack-remote, which re-checks access server side. */
  import { onMount, untrack } from "svelte";
  import SlackTargetFields from "./SlackTargetFields.svelte";
  import SlackIdentityFields from "./SlackIdentityFields.svelte";
  import SlackListenFields from "./SlackListenFields.svelte";
  import {
    getSlackRemote, updateSlackRemote, testSlackRemote, listAgentConnectors, runApi,
    type AgentItem, type SlackIdentity, type SlackListen, type SlackRemoteConfig,
    type SlackRemoteInfo, type SlackTarget, type SlackTestResult,
  } from "../../api/team.js";
  import { cleanConfig, configError, patchBody, slackTestSummary } from "../../slackRemote.js";

  type Props = {
    base: string;
    agent: AgentItem;
    section: "remote" | "advanced";
    /** The server's latest settings, so the roster's copy follows. */
    onChanged: (info: SlackRemoteInfo) => void;
  };
  let { base, agent, section, onChanged }: Props = $props();

  let info = $state<SlackRemoteInfo | null>(untrack(() => agent.slack_remote ?? null));
  let loadError = $state("");
  let workspace = $state("");
  let busy = $state<"" | "save" | "test">("");
  let error = $state("");
  let note = $state("");
  let testResult = $state<SlackTestResult | null>(null);

  let target = $state<SlackTarget>("dm");
  let channel = $state("");
  let user = $state("");
  let mentionId = $state("");
  let threadTs = $state("");
  let targetName = $state("");
  let identity = $state<SlackIdentity>("bot");
  let accountId = $state("");
  let listen = $state<SlackListen>("target");
  let marker = $state(true);
  let mention = $state(true);
  let idleSec = $state(0);
  let maxSec = $state(0);

  function take(r: SlackRemoteInfo) {
    info = r;
    target = r.target;
    channel = r.channel ?? "";
    user = r.user ?? "";
    mentionId = r.mention_id ?? "";
    threadTs = r.thread_ts ?? "";
    targetName = r.target_name ?? "";
    identity = r.identity;
    accountId = r.account_id ?? "";
    listen = r.listen === "anyone" ? "anyone" : "target";
    marker = r.marker;
    mention = r.mention_target ?? true;
    idleSec = r.idle_sec ?? 0;
    maxSec = r.max_sec ?? 0;
  }
  untrack(() => { if (info) take(info); });

  onMount(() => {
    runApi(getSlackRemote(base, agent.id))
      .then(take)
      .catch((e) => { loadError = e instanceof Error ? e.message : String(e); });
    runApi(listAgentConnectors(base))
      .then((all) => { workspace = (all ?? []).find((c) => c.id === info?.connector_id)?.label ?? ""; })
      .catch(() => {});
  });

  const form = $derived<SlackRemoteConfig | null>(
    info
      ? cleanConfig({
          connector_id: info.connector_id, identity, account_id: accountId, target, channel, user,
          mention_id: mentionId, thread_ts: threadTs, target_name: targetName, listen, marker, mention_target: mention,
          idle_sec: idleSec, max_sec: maxSec,
        })
      : null,
  );
  const stored = $derived(info ? cleanConfig({ ...info, usage: undefined }) : null);
  const dirty = $derived(!!form && JSON.stringify(form) !== JSON.stringify(stored));
  const why = $derived(form ? configError(form) : "");

  async function run(kind: typeof busy, fn: () => Promise<SlackRemoteInfo>, done: string) {
    if (busy) return;
    busy = kind;
    error = "";
    note = "";
    try {
      const r = await fn();
      take(r);
      onChanged(r);
      note = done;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  function save() {
    if (!form || why || !dirty) return;
    const body = patchBody(form);
    void run("save", () => runApi(updateSlackRemote(base, agent.id, body)), "Saved.");
  }


  async function test() {
    if (busy) return;
    busy = "test";
    testResult = null;
    error = "";
    try {
      testResult = await runApi(testSlackRemote(base, { agent_id: agent.id }));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = "";
    }
  }

  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const primary =
    "rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50";
  const outline =
    "rounded-lg border border-white-300 px-3 py-1.5 text-sm text-black-900 hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-600";
</script>

{#if !info}
  <p class="text-sm {loadError ? 'text-neg-400' : 'text-black-800 dark:text-black-600'}">{loadError || "Loading…"}</p>
{:else if section === "remote"}
  <div data-testid="slack-remote-settings" class="space-y-4">
    <div>
      <p class="text-sm font-semibold text-black-900 dark:text-white-100">Remote Slack</p>
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">
        Workspace: <span class="font-medium">{workspace || info.connector_id}</span>
        {#if info.session?.thread_ts} · this chat's thread {info.session.thread_ts}{/if}
      </p>
    </div>
    <p class="rounded-xl border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-500 dark:bg-navy-800 dark:text-amber-300" data-testid="slack-remote-warning">{info.warning}</p>
    <SlackTargetFields {base} connectorId={info.connector_id} {identity} {accountId} bind:target bind:channel bind:user bind:mentionId bind:threadTs bind:targetName idPrefix="ss" />
    <SlackIdentityFields {base} connectorId={info.connector_id} bind:identity bind:accountId idPrefix="ss" />
    <SlackListenFields bind:listen bind:marker bind:mention bind:idleSec bind:maxSec idPrefix="ss" />
    <div class="flex flex-wrap items-center gap-2">
      <button type="button" class={primary} disabled={!dirty || !!why || !!busy} data-testid="ss-save" onclick={save}>{busy === "save" ? "Saving…" : "Save"}</button>
      <button type="button" class={outline} disabled={!!busy || dirty} data-testid="ss-test" onclick={test}>{busy === "test" ? "Waiting for a reply…" : "Test"}</button>
      {#if dirty}<span class="text-xs text-black-800 dark:text-black-600">{why || "Save first — Test uses the saved settings."}</span>{/if}
    </div>
    {#if testResult}
      <p class="text-xs {testResult.ok ? 'text-green-600 dark:text-green-400' : 'text-neg-400'}" data-testid="ss-test-result">{slackTestSummary(testResult)}</p>
    {/if}
  </div>
{:else}
  <div class="space-y-4" data-testid="slack-remote-advanced">
    <p class="text-sm font-semibold text-black-900 dark:text-white-100">Advanced</p>
  </div>
{/if}
{#if error}<p class="mt-2 text-sm text-neg-400">{error}</p>{/if}
{#if note && !error}<p class="mt-2 text-xs text-green-600 dark:text-green-400">{note}</p>{/if}
