<script lang="ts">
  /* Header badge + kebab for a plugin installed from a source (tool / job /
     service detail pages). "Update available" shows to everyone; the kebab
     with "Update to vX" and "Check now" is admin-only — updates live here, not
     on the Admin → Plugins dashboard. Renders nothing for built-ins. */
  import { KebabMenu } from "@wick-fe/common-ui";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { getPluginSourceStatus, checkPluginSource, updatePluginStream, type PluginSourceStatus } from "$lib/api.js";

  type Props = { pluginKey: string; onUpdated?: () => void };
  let { pluginKey, onUpdated }: Props = $props();

  let st = $state<PluginSourceStatus | null>(null);
  let busy = $state("");

  async function load(): Promise<void> {
    try {
      st = await getPluginSourceStatus(pluginKey);
    } catch {
      st = null; // built-in or not recorded: no badge
    }
  }
  $effect(() => {
    void pluginKey;
    load();
  });

  async function update(): Promise<void> {
    busy = "update";
    try {
      await updatePluginStream(pluginKey, () => {});
      toastOk(`Updated ${pluginKey} to v${st?.available_version}`);
      await load();
      onUpdated?.();
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  async function checkNow(): Promise<void> {
    if (!st?.source_id) return;
    busy = "check";
    try {
      await checkPluginSource(st.source_id);
      await load();
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }

  let items = $derived.by(() => {
    const out = [];
    if (st?.available_version) {
      out.push({ label: busy === "update" ? "Updating…" : `Update to v${st.available_version}`, onclick: update, disabled: busy !== "", hint: st.source_name ? `from ${st.source_name}` : undefined });
    }
    out.push({ label: busy === "check" ? "Checking…" : "Check for updates", onclick: checkNow, disabled: busy !== "" || !st?.source_id });
    return out;
  });
</script>

{#if st?.source_id}
  <div class="flex items-center gap-2" data-testid="plugin-update-menu">
    {#if st.available_version}
      <span class="rounded-full border border-amber-300 bg-amber-50 dark:bg-amber-900 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300" data-testid="update-badge">Update available · v{st.available_version}</span>
    {/if}
    {#if st.is_admin}
      <KebabMenu ariaLabel={`Plugin actions for ${pluginKey}`} {items} />
    {/if}
  </div>
{/if}
