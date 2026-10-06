<script lang="ts">
  /* Auth of an A2A remote agent: none, a Bearer token or an API key in a
     named header. The secret is write-only — the server answers with
     auth_set, never the value — so the field always starts empty and
     `saved` only says one is on file. */
  import { AUTH_OPTIONS, DEFAULT_API_KEY_HEADER } from "../../remoteAgent.js";
  import type { RemoteAuthType } from "../../api/team.js";

  type Props = {
    type: RemoteAuthType;
    secret: string;
    header: string;
    /** A secret is stored for this type already (Settings). */
    saved?: boolean;
    idPrefix: string;
  };
  let { type = $bindable(), secret = $bindable(), header = $bindable(), saved = false, idPrefix }: Props = $props();

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
</script>

<div class="space-y-2" data-testid="{idPrefix}-auth">
  <span class={label}>Auth</span>
  <div class="inline-flex rounded-lg border border-white-300 p-0.5 dark:border-navy-600" role="group" aria-label="Auth type">
    {#each AUTH_OPTIONS as o (o.value)}
      <button
        type="button"
        class="rounded-md px-3 py-1 text-xs {type === o.value ? 'bg-green-500 text-white-100' : 'text-black-800 dark:text-black-600'}"
        aria-pressed={type === o.value}
        onclick={() => { type = o.value; secret = ""; }}
      >{o.label}</button>
    {/each}
  </div>
  {#if type === "api_key"}
    <div>
      <label class={label} for="{idPrefix}-auth-header">Header</label>
      <input id="{idPrefix}-auth-header" class="{input} font-mono" bind:value={header} placeholder={DEFAULT_API_KEY_HEADER} />
    </div>
  {/if}
  {#if type !== "none"}
    <div>
      <label class={label} for="{idPrefix}-auth-secret">{type === "bearer" ? "Token" : "Key"}</label>
      <input
        id="{idPrefix}-auth-secret"
        class="{input} font-mono"
        type="password"
        autocomplete="off"
        bind:value={secret}
        placeholder={saved ? "•••••• saved — type to replace" : "Paste the secret"}
      />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">Stored encrypted and never shown again.</p>
    </div>
  {/if}
</div>
