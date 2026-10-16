<script lang="ts">
  /* "What it can do" card on a plugin's detail page (every kind): the Host
     API scopes it declares with their status and item counts, linking to
     Admin → Plugins → Host access. Rendered only for admins and only for
     installed plugins: both come from the installed-plugins list (readable
     by everyone), so a built-in or a non-admin never fires a request the
     admin API would refuse. */
  import { getHostAccess, listInstalledPlugins, type PluginHostAccess } from "$lib/api.js";
  import { STATUS_LABEL, statusClass, scopeTone, summary, hostAccessLink } from "./hostAccess.js";

  let { pluginKey }: { pluginKey: string } = $props();
  let data = $state<PluginHostAccess | null>(null);

  $effect(() => {
    const key = pluginKey;
    data = null;
    // Promise.resolve first: a mocked or missing API must not throw here.
    Promise.resolve()
      .then(() => listInstalledPlugins())
      .then((inst) => (inst?.is_admin && inst.plugins?.some((p) => p.key === key) ? getHostAccess(key) : null))
      .then((v) => { if (key === pluginKey) data = v ?? null; })
      .catch(() => {});
  });
</script>

{#if data}
  <section data-testid="host-access-card">
    <h2 class="text-base font-semibold text-black-900 dark:text-white-100">What it can do</h2>
    <div class="mt-3 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4 text-sm">
      <div class="flex flex-wrap items-center gap-3">
        <p class="flex-1 text-black-900 dark:text-white-100" data-testid="host-access-summary">{summary(data)}</p>
        {#if data.blocked_all}<span class={statusClass("blocked")}>all host access blocked</span>{/if}
        {#if data.revoked}<span class={statusClass("blocked")} data-testid="card-revoked">host access revoked</span>{/if}
        <a class="text-xs text-black-800 underline dark:text-black-600" href={hostAccessLink(data.key)}>Manage host access</a>
      </div>
      {#if data.scopes.length}
        <ul class="mt-3 space-y-1">
          {#each data.scopes as s (s.scope)}
            <li class="flex items-center gap-2 text-xs">
              <span class={`flex-1 ${scopeTone(s)}`}>{s.label || s.scope} <code class="font-mono text-black-700 dark:text-black-600">{s.scope}</code>{#if s.items?.length} · {s.items.length} item{s.items.length === 1 ? "" : "s"}{/if}</span>
              <span class={statusClass(s.status)}>{STATUS_LABEL[s.status] ?? s.status}</span>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  </section>
{/if}
