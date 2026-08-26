<script lang="ts">
  /* Integrations for the ticket system: outbound webhooks (wick → your
     system) and the token-authed REST surface (your system → wick).

     Kept in its own component because the two halves are independent of
     everything above them in the ticket section — a team may run the board
     entirely from the UI and never open this. */
  import { onMount } from "svelte";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { getTicketEvents, getWebhookDeliveries, testWebhook } from "$lib/api.js";
  import {
    SECRET_REDACTED,
    type TicketDelivery,
    type TicketIntegrations,
    type TicketWebhook,
  } from "$lib/types.js";

  type Props = {
    projectID: string;
    base: string;
    cfg: TicketIntegrations;
    onChange: (cfg: TicketIntegrations) => void;
  };

  let { projectID, base, cfg, onChange }: Props = $props();

  const apiEnabled = $derived(cfg.api_enabled === true);
  const webhooks = $derived(cfg.webhooks ?? []);

  /* The catalogue comes from the server so the picker can never offer an
     event that does not fire. */
  let events = $state<string[]>([]);
  onMount(async () => {
    events = await getTicketEvents();
  });

  /* Delivery log + test result, per webhook id. Loaded on demand: a project
     with several endpoints should not fetch every log to draw the section. */
  let deliveries = $state<Record<string, TicketDelivery[]>>({});
  let testing = $state<Record<string, boolean>>({});
  let expanded = $state<Record<string, boolean>>({});

  function patch(p: Partial<TicketIntegrations>) {
    onChange({ ...cfg, ...p });
  }

  function patchHook(i: number, p: Partial<TicketWebhook>) {
    patch({ webhooks: webhooks.map((w, idx) => (idx === i ? { ...w, ...p } : w)) });
  }

  function addHook() {
    /* No id: the server mints one on save, which is what keeps the delivery
       log attached across later edits. */
    patch({
      webhooks: [
        ...webhooks,
        { id: "", name: "", url: "", secret: "", events: [], enabled: true },
      ],
    });
  }

  function removeHook(i: number) {
    patch({ webhooks: webhooks.filter((_, idx) => idx !== i) });
  }

  /* Empty events list = every event, so the "All events" checkbox is the
     absence of a filter rather than a list of all of them. Storing them all
     explicitly would silently stop delivering anything added later. */
  function allEvents(w: TicketWebhook): boolean {
    return (w.events ?? []).length === 0;
  }

  function toggleAllEvents(i: number, all: boolean) {
    patchHook(i, { events: all ? [] : [...events] });
  }

  function toggleEvent(i: number, name: string, on: boolean) {
    const cur = webhooks[i].events ?? [];
    const next = on ? [...cur, name] : cur.filter((e) => e !== name);
    /* Unchecking the last one would read as "all events", which is the
       opposite of what the operator just asked for — keep one. */
    patchHook(i, { events: next.length === 0 ? [name] : next });
  }

  const signed = (w: TicketWebhook) => (w.secret ?? "") !== "";

  function clearSecret(i: number) {
    patchHook(i, { secret: "" });
  }

  async function runTest(w: TicketWebhook) {
    if (!w.id) {
      toastError("Save the webhook first — the test sends to the stored endpoint.");
      return;
    }
    testing = { ...testing, [w.id]: true };
    try {
      const res = await testWebhook(projectID, w.id);
      if (res.ok) {
        toastOk(`Delivered — HTTP ${res.status}`);
      } else {
        toastError(`Failed: ${res.error || `HTTP ${res.status}`}`);
      }
      deliveries = { ...deliveries, [w.id]: await getWebhookDeliveries(projectID, w.id) };
      expanded = { ...expanded, [w.id]: true };
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Test failed");
    } finally {
      testing = { ...testing, [w.id]: false };
    }
  }

  async function toggleLog(w: TicketWebhook) {
    const open = !expanded[w.id];
    expanded = { ...expanded, [w.id]: open };
    if (open && w.id && deliveries[w.id] === undefined) {
      deliveries = { ...deliveries, [w.id]: await getWebhookDeliveries(projectID, w.id) };
    }
  }

  function urlError(w: TicketWebhook): string {
    const u = (w.url ?? "").trim();
    if (u === "") return "url is required";
    try {
      const parsed = new URL(u);
      if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
        return "must be http or https";
      }
      return "";
    } catch {
      return "not a valid URL";
    }
  }

  /* Shown next to the API toggle so the curl in the docs can be copied with
     the right host already filled in. The machine API is mounted at the wick
     root (/api), not under the tool — see TicketRESTShim. */
  const apiBase = $derived(`${window.location.origin}/api`);

  async function copy(text: string, label: string) {
    try {
      await navigator.clipboard.writeText(text);
      toastOk(`${label} copied`);
    } catch {
      toastError("Could not copy — select and copy manually");
    }
  }

  /* Full API reference, webhook payloads, and signature-verification
     samples live on the docs site rather than being restated here: one
     copy cannot drift from the other, and a settings form is the wrong
     place to read documentation. */
  const DOCS_URL =
    "https://yogasw.github.io/wick/guide/agents/ticket-integrations.html";

  const inputClass =
    "w-full rounded-lg border border-white-300 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100";
  const labelClass =
    "mb-1 block text-[10px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600";
</script>

<div class="space-y-6">
  <!-- ── REST API ──────────────────────────────────────────────── -->
  <div class="space-y-2">
    <div class="flex items-center justify-between gap-3 rounded-lg border border-white-300 bg-white-200 px-4 py-3 dark:border-navy-600 dark:bg-navy-800">
      <div class="min-w-0">
        <span class="block text-sm font-medium text-black-900 dark:text-white-100">
          {apiEnabled ? "REST API is on" : "REST API is off"}
        </span>
        <span class="mt-0.5 block text-[11px] leading-relaxed text-black-700 dark:text-black-600">
          Lets a Personal Access Token create and move this project's tickets. The
          board keeps working either way — this only governs machine access.
        </span>
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={apiEnabled}
        aria-label="Enable ticket REST API"
        onclick={() => patch({ api_enabled: !apiEnabled })}
        class={"relative h-6 w-11 shrink-0 rounded-full transition-colors " + (apiEnabled ? "bg-green-500" : "bg-white-400 dark:bg-navy-600")}
      >
        <span class={"absolute top-0.5 h-5 w-5 rounded-full bg-white-100 shadow-sm transition-all " + (apiEnabled ? "left-[22px]" : "left-0.5")}></span>
      </button>
    </div>

    {#if apiEnabled}
      <div class="rounded-lg border border-white-300 bg-white-200 p-3 dark:border-navy-600 dark:bg-navy-800">
        <p class={labelClass}>Base URL</p>
        <div class="flex items-center gap-2">
          <code class="min-w-0 flex-1 truncate rounded-lg bg-white-100 px-2 py-1.5 font-mono text-xs text-black-900 dark:bg-navy-700 dark:text-white-100">
            {apiBase}
          </code>
          <button
            type="button"
            onclick={() => copy(apiBase, "Base URL")}
            class="shrink-0 rounded-lg border border-white-300 px-2 py-1.5 text-xs font-medium text-black-800 transition-colors hover:bg-white-100 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-700"
          >
            Copy
          </button>
        </div>
        <p class="mt-2 text-[11px] leading-relaxed text-black-700 dark:text-black-600">
          Authenticate with <code class="font-mono">Authorization: Bearer &lt;token&gt;</code>.
          Create a token on the
          <a href="/profile/tokens" class="text-link-400 hover:underline">Access tokens</a>
          page — it acts as you, so it reaches only the projects you can see.
          Project id: <code class="font-mono">{projectID}</code>
        </p>
      </div>

      <!-- The endpoint list, payload shapes, and verification samples live
           in the docs. Linked rather than restated so the two cannot drift. -->
      <a
        href={DOCS_URL}
        target="_blank"
        rel="noreferrer"
        class="flex items-center gap-2 rounded-lg border border-white-300 bg-white-200 px-3 py-2.5 transition-colors hover:bg-white-100 dark:border-navy-600 dark:bg-navy-800 dark:hover:bg-navy-700"
      >
        <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M4 2h5l3 3v9H4z" stroke-linecap="round" stroke-linejoin="round"></path>
          <path d="M6 8h4M6 10.5h4" stroke-linecap="round"></path>
        </svg>
        <span class="min-w-0 flex-1">
          <span class="block text-xs font-medium text-black-900 dark:text-white-100">
            API reference &amp; webhook payloads
          </span>
          <span class="mt-0.5 block text-[11px] leading-relaxed text-black-700 dark:text-black-600">
            Every endpoint with a curl, all event payloads, and signature
            verification in Go, Node, and Python.
          </span>
        </span>
        <span class="shrink-0 text-xs text-link-400">Docs ↗</span>
      </a>
    {/if}
  </div>

  <!-- ── Webhooks ──────────────────────────────────────────────── -->
  <div class="space-y-2">
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <p class="text-xs font-medium text-black-800 dark:text-black-600">Webhooks</p>
        <p class="mt-1 text-[11px] leading-relaxed text-black-700 dark:text-black-600">
          Wick POSTs a JSON event to each endpoint when a ticket changes. Deliveries
          retry three times over about 30 seconds, then give up — a receiver that
          must never miss an event should reconcile through the REST API on startup.
          <a
            href={`${DOCS_URL}#webhooks`}
            target="_blank"
            rel="noreferrer"
            class="text-link-400 hover:underline"
          >Payload shapes and signature verification ↗</a>
        </p>
      </div>
      <button
        type="button"
        onclick={addHook}
        class="shrink-0 rounded-lg border border-white-300 px-2.5 py-1.5 text-xs font-medium text-black-800 transition-colors hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-800"
      >
        Add webhook
      </button>
    </div>

    {#if webhooks.length === 0}
      <p class="rounded-lg border border-dashed border-white-400 px-4 py-6 text-center text-xs text-black-700 dark:border-navy-600 dark:text-black-600">
        No webhooks yet. Nothing is sent anywhere.
      </p>
    {/if}

    {#each webhooks as w, i (i)}
      {@const err = urlError(w)}
      {@const isSigned = signed(w)}
      <div class="rounded-lg border border-white-300 bg-white-200 p-3 dark:border-navy-600 dark:bg-navy-800">
        <div class="grid gap-2 sm:grid-cols-[1fr_2fr]">
          <label class="block">
            <span class={labelClass}>Name</span>
            <input
              value={w.name ?? ""}
              placeholder="Helpdesk sync"
              aria-label="Webhook name"
              oninput={(e) => patchHook(i, { name: (e.target as HTMLInputElement).value })}
              class={inputClass}
            />
          </label>
          <label class="block">
            <span class={labelClass}>Endpoint URL</span>
            <input
              value={w.url}
              placeholder="https://abc.com/hooks/wick-tickets"
              aria-label="Webhook URL"
              oninput={(e) => patchHook(i, { url: (e.target as HTMLInputElement).value })}
              class={inputClass + (err ? " border-neg-400 dark:border-neg-400" : "")}
            />
          </label>
        </div>
        {#if err}
          <p class="mt-1 text-[11px] text-neg-400">{err}</p>
        {/if}

        <!-- Signing secret. Never rendered back, so the field shows state
             rather than the value. -->
        <div class="mt-2">
          <span class={labelClass}>Signing secret</span>
          {#if isSigned && w.secret === SECRET_REDACTED}
            <div class="flex items-center gap-2">
              <span class="inline-flex items-center gap-1.5 rounded-lg bg-pos-400/10 px-2 py-1.5 text-xs font-medium text-pos-400">
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M4 7V5a4 4 0 018 0v2M3 7h10v6H3z" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
                Signed — key hidden
              </span>
              <button
                type="button"
                onclick={() => clearSecret(i)}
                class="rounded-lg border border-white-300 px-2 py-1.5 text-xs font-medium text-black-800 transition-colors hover:bg-white-100 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-700"
              >
                Replace
              </button>
            </div>
          {:else}
            <input
              value={w.secret ?? ""}
              type="password"
              placeholder="Leave empty to send unsigned"
              aria-label="Webhook signing secret"
              oninput={(e) => patchHook(i, { secret: (e.target as HTMLInputElement).value })}
              class={inputClass}
            />
            <p class="mt-1 text-[11px] leading-relaxed text-black-700 dark:text-black-600">
              Signs the body as <code class="font-mono">X-Wick-Signature: sha256=…</code>
              so a receiver can prove the request came from wick. Strongly recommended
              on any endpoint reachable from the internet.
            </p>
          {/if}
        </div>

        <!-- Event filter. -->
        <div class="mt-2">
          <span class={labelClass}>Events</span>
          <label class="flex items-center gap-2 text-xs text-black-900 dark:text-white-100">
            <input
              type="checkbox"
              checked={allEvents(w)}
              onchange={(e) => toggleAllEvents(i, (e.target as HTMLInputElement).checked)}
              class="h-4 w-4 rounded border-white-400 text-green-500 dark:border-navy-600"
            />
            All events
            <span class="text-black-700 dark:text-black-600">(including any added later)</span>
          </label>

          {#if !allEvents(w)}
            <div class="mt-2 grid gap-1 sm:grid-cols-2">
              {#each events as name (name)}
                <label class="flex items-center gap-2 text-xs text-black-900 dark:text-white-100">
                  <input
                    type="checkbox"
                    checked={(w.events ?? []).includes(name)}
                    onchange={(e) => toggleEvent(i, name, (e.target as HTMLInputElement).checked)}
                    class="h-4 w-4 rounded border-white-400 text-green-500 dark:border-navy-600"
                  />
                  <code class="font-mono text-[11px]">{name}</code>
                </label>
              {/each}
            </div>
          {/if}
        </div>

        <!-- Row footer: enabled, test, log, remove. -->
        <div class="mt-3 flex flex-wrap items-center gap-2 border-t border-white-300 pt-2 dark:border-navy-600">
          <label class="flex items-center gap-2 text-xs text-black-900 dark:text-white-100">
            <input
              type="checkbox"
              checked={w.enabled}
              onchange={(e) => patchHook(i, { enabled: (e.target as HTMLInputElement).checked })}
              class="h-4 w-4 rounded border-white-400 text-green-500 dark:border-navy-600"
            />
            Enabled
          </label>

          <span class="flex-1"></span>

          <button
            type="button"
            disabled={testing[w.id] === true}
            onclick={() => runTest(w)}
            class="rounded-lg border border-white-300 px-2 py-1.5 text-xs font-medium text-black-800 transition-colors hover:bg-white-100 disabled:opacity-50 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-700"
          >
            {testing[w.id] ? "Sending…" : "Send test"}
          </button>
          {#if w.id}
            <button
              type="button"
              onclick={() => toggleLog(w)}
              class="rounded-lg border border-white-300 px-2 py-1.5 text-xs font-medium text-black-800 transition-colors hover:bg-white-100 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-700"
            >
              {expanded[w.id] ? "Hide log" : "Recent deliveries"}
            </button>
          {/if}
          <button
            type="button"
            onclick={() => removeHook(i)}
            aria-label="Remove webhook"
            class="rounded-lg border border-white-300 px-2 py-1.5 text-xs font-medium text-neg-400 transition-colors hover:bg-white-100 dark:border-navy-600 dark:hover:bg-navy-700"
          >
            Remove
          </button>
        </div>

        {#if w.id && expanded[w.id]}
          <div class="mt-2 space-y-1">
            {#if (deliveries[w.id] ?? []).length === 0}
              <p class="text-[11px] text-black-700 dark:text-black-600">
                No deliveries recorded yet.
              </p>
            {:else}
              {#each deliveries[w.id] as d (d.at + d.event)}
                <div class="flex items-center gap-2 rounded-lg bg-white-100 px-2 py-1.5 text-[11px] dark:bg-navy-700">
                  <span class={"inline-block h-2 w-2 shrink-0 rounded-full " + (d.ok ? "bg-pos-400" : "bg-neg-400")}></span>
                  <code class="font-mono text-black-900 dark:text-white-100">{d.event}</code>
                  <span class="text-black-700 dark:text-black-600">
                    {d.status ? `HTTP ${d.status}` : d.error}
                  </span>
                  <span class="flex-1"></span>
                  <span class="text-black-700 dark:text-black-600">
                    {d.attempts > 1 ? `${d.attempts} attempts · ` : ""}{new Date(d.at).toLocaleString()}
                  </span>
                </div>
              {/each}
            {/if}
          </div>
        {/if}
      </div>
    {/each}
  </div>
</div>
