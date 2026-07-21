<script lang="ts">
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import {
    listBrowserSessions,
    closeBrowserSession,
    type BrowserSession,
  } from "../api.js";

  /* key is fixed to playwright_browser — this section only renders for it. */
  type Props = { connectorId: string };
  let { connectorId }: Props = $props();

  let sessions = $state<BrowserSession[]>([]);
  let loading = $state(true);
  let loaded = $state(false);
  let busy = $state("");
  let expanded = $state<Record<string, boolean>>({});

  async function refresh() {
    loading = true;
    try {
      sessions = await listBrowserSessions(connectorId);
      loaded = true;
    } catch (e) {
      toastError("Load sessions failed", e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }

  async function kill(sid: string) {
    if (busy) return;
    busy = sid;
    try {
      await closeBrowserSession(connectorId, sid);
      toastOk("Session closed");
      await refresh();
    } catch (e) {
      toastError("Close failed", e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  function toggle(sid: string) {
    expanded = { ...expanded, [sid]: !expanded[sid] };
  }

  /* "Goto" opens the conversation live-browser panel pre-pointed at this
   * session (BrowserPanel reads these query params on mount). */
  function gotoURL(sid: string): string {
    const q = new URLSearchParams({ browser_instance: connectorId, browser_session: sid });
    return `/tools/agents/connectors?${q.toString()}`;
  }

  function fmtWhen(iso: string): string {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
  }

  $effect(() => {
    refresh();
  });
</script>

<section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
  <div class="flex items-start justify-between gap-4 px-5 py-4">
    <div>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Active sessions</h2>
      <p class="mt-1 text-sm text-black-800 dark:text-black-600">
        Live browser sessions currently open on this connector. Inspect their tabs, open the live view, or kill one to free its browser process.
      </p>
    </div>
    <button
      type="button"
      class="shrink-0 rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-700 dark:text-white-200 hover:border-green-400 transition-colors disabled:opacity-50"
      disabled={loading}
      onclick={refresh}
      data-testid="sessions-refresh"
    >{loading ? "Loading…" : "Refresh"}</button>
  </div>

  <div class="border-t border-white-300 dark:border-navy-600 px-5 py-4 space-y-2">
    {#if !loaded && loading}
      <p class="text-sm text-black-700 dark:text-black-600">Loading…</p>
    {:else if sessions.length === 0}
      <p class="text-sm text-black-700 dark:text-black-600">No active sessions.</p>
    {:else}
      {#each sessions as s (s.session_id)}
        <div class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-50 dark:bg-navy-800" data-testid="session-row">
          <div class="flex items-center gap-3 px-4 py-2.5">
            <!-- Clickable session id → opens the live view for this session -->
            <a
              href={gotoURL(s.session_id)}
              class="font-mono text-xs font-medium text-green-700 dark:text-green-400 hover:underline truncate"
              title="Open live view for this session"
              data-testid="session-goto"
            >{s.session_id}</a>
            <span class="shrink-0 rounded-full bg-white-300 dark:bg-navy-700 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-500">{s.browser || "chromium"}</span>
            <span class="shrink-0 text-[11px] text-black-600 dark:text-black-500">{s.tabs?.length ?? 0} tab{(s.tabs?.length ?? 0) === 1 ? "" : "s"}</span>

            <div class="ml-auto flex items-center gap-3">
              <button
                type="button"
                class="text-[11px] font-medium text-black-700 dark:text-white-200 hover:underline"
                onclick={() => toggle(s.session_id)}
                data-testid="session-inspect"
              >{expanded[s.session_id] ? "Hide" : "Inspect"}</button>
              <button
                type="button"
                class="text-[11px] font-medium text-neg-400 hover:underline disabled:opacity-50"
                disabled={busy === s.session_id}
                onclick={() => kill(s.session_id)}
                data-testid="session-kill"
              >{busy === s.session_id ? "Killing…" : "Kill"}</button>
            </div>
          </div>

          {#if expanded[s.session_id]}
            <div class="border-t border-white-300 dark:border-navy-600 px-4 py-2.5 space-y-2">
              <dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-xs">
                <dt class="text-black-600 dark:text-black-500">PID</dt>
                <dd class="font-mono text-black-900 dark:text-white-100">{s.pid}</dd>
                <dt class="text-black-600 dark:text-black-500">Engine</dt>
                <dd class="text-black-900 dark:text-white-100">{s.browser || "chromium"}</dd>
                <dt class="text-black-600 dark:text-black-500">Created</dt>
                <dd class="text-black-900 dark:text-white-100">{fmtWhen(s.created)}</dd>
              </dl>

              {#if s.tabs && s.tabs.length > 0}
                <div class="space-y-1">
                  <p class="text-[11px] font-medium text-black-700 dark:text-white-200">Tabs</p>
                  {#each s.tabs as t (t.index)}
                    <div class="flex items-start gap-2 text-xs">
                      <span class="shrink-0 font-mono text-black-600 dark:text-black-500">{t.index}</span>
                      <div class="min-w-0">
                        <p class="text-black-900 dark:text-white-100 truncate">{t.title || "(untitled)"}</p>
                        <p class="text-[11px] text-black-600 dark:text-black-500 truncate">{t.url}</p>
                      </div>
                    </div>
                  {/each}
                </div>
              {:else}
                <p class="text-[11px] text-black-600 dark:text-black-500">No open tabs reported.</p>
              {/if}
            </div>
          {/if}
        </div>
      {/each}
    {/if}
  </div>
</section>
