<script lang="ts">
  /* The per-agent config a service plugin declares (RemoteConfigs), shared by
     the Plugin wizard and the agent's Remote settings. Required fields carry
     "*", the rest "optional": left empty, the plugin falls back to its own
     setting. A stored secret never comes back — blank keeps it. */
  import type { PluginRemoteField } from "../../api/team.js";

  type Props = { fields: PluginRemoteField[]; values: Record<string, string>; idPrefix?: string };
  let { fields, values = $bindable(), idPrefix = "prc" }: Props = $props();

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 flex items-center gap-1.5 text-xs font-medium text-black-800 dark:text-black-600";

  function options(f: PluginRemoteField): { value: string; label: string }[] {
    return (f.options ?? "")
      .split("|")
      .filter(Boolean)
      .map((o) => {
        const [l, v] = o.split("::");
        return { label: l, value: v ?? l };
      });
  }

  function set(key: string, v: string) {
    values = { ...values, [key]: v };
  }
</script>

{#if fields.length > 0}
  <div class="space-y-3" data-testid="plugin-remote-config">
    {#each fields as f (f.key)}
      <div data-testid="plugin-config-{f.key}">
        <label class={label} for="{idPrefix}-{f.key}">
          <span class="font-mono">{f.key}</span>
          {#if f.required}
            <span class="text-red-500" aria-label="required">*</span>
          {:else}
            <span class="text-[10px] font-normal text-black-700 dark:text-black-600" data-testid="plugin-config-optional">optional</span>
          {/if}
        </label>
        {#if f.type === "dropdown"}
          <select id="{idPrefix}-{f.key}" class={input} value={values[f.key] ?? ""} onchange={(e) => set(f.key, (e.currentTarget as HTMLSelectElement).value)}>
            <option value="">{f.required ? "Choose…" : "Plugin setting"}</option>
            {#each options(f) as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
          </select>
        {:else}
          <input
            id="{idPrefix}-{f.key}"
            class="{input} {f.is_secret ? 'font-mono' : ''}"
            type={f.is_secret ? "password" : "text"}
            autocomplete="off"
            value={values[f.key] ?? ""}
            placeholder={f.is_secret && f.has_value ? "Stored — leave blank to keep" : ""}
            oninput={(e) => set(f.key, (e.currentTarget as HTMLInputElement).value)}
          />
        {/if}
        {#if f.description || !f.required}
          <p class="mt-1 text-xs text-black-800 dark:text-black-600">
            {f.description ?? ""}{#if !f.required}{f.description ? " " : ""}Empty = use the plugin's setting.{/if}
          </p>
        {/if}
      </div>
    {/each}
  </div>
{/if}
