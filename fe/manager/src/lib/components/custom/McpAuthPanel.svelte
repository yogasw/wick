<script lang="ts">
  /* Auth scheme selector + the matching input panel. One of
     none / bearer / custom_header / oauth / sso is active at a time;
     switching schemes preserves the extra header rows (the parent keeps
     all fields). Mirrors the auth-scheme radios + the five panels in
     custom_mcp_form.templ, with the JS toggle logic folded into
     reactive {#if} blocks here. */
  import { TextInput } from "@wick-fe/common-ui";
  import McpHeadersEditor from "./McpHeadersEditor.svelte";
  import type { McpServerForm } from "$lib/types.js";

  type Props = {
    form: McpServerForm;
    oauthConnected: boolean;
    onChange: () => void;
  };
  let { form, oauthConnected, onChange }: Props = $props();

  const schemes: Array<{ value: string; label: string }> = [
    { value: "none", label: "None" },
    { value: "bearer", label: "Bearer token" },
    { value: "custom_header", label: "Custom header" },
    { value: "oauth", label: "OAuth (login on the server)" },
    { value: "sso", label: "SSO (forward caller's session)" },
  ];

  const ttlOptions: Array<{ value: number; label: string }> = [
    { value: 60, label: "1 min" },
    { value: 300, label: "5 min (default)" },
    { value: 900, label: "15 min" },
  ];

  const inputClass =
    "mt-1 w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-2 font-mono text-sm text-black-900 dark:text-white-100 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800";

  const ssoClaimExample = `{
    "sub":    "user id (uuid)",
    "email":  "user email",
    "name":   "user display name",
    "groups": "user tag ids",
    "aud":    "audience below",
    "iss":    "this wick's app URL",
    "iat":    "now", "exp": "now + TTL"
  }`;

  function selectScheme(value: string) {
    form.auth_scheme = value;
    onChange();
  }

  function choiceClass(active: boolean): string {
    return active
      ? "border-green-500"
      : "border-white-400 dark:border-navy-600";
  }
</script>

<div class="mt-6">
  <span class="block text-xs font-medium text-black-800 dark:text-black-600">Auth scheme</span>
  <div class="mt-2 flex flex-wrap gap-3" role="radiogroup" aria-label="Auth scheme">
    {#each schemes as s (s.value)}
      <label class="flex cursor-pointer items-center gap-2 rounded-lg border bg-white-100 dark:bg-navy-800 px-3 py-2 {choiceClass(form.auth_scheme === s.value)}">
        <input
          type="radio"
          name="cc-auth-scheme"
          class="accent-green-500"
          value={s.value}
          checked={form.auth_scheme === s.value}
          onchange={() => selectScheme(s.value)}
        />
        <span class="text-sm font-medium text-black-900 dark:text-white-100">{s.label}</span>
      </label>
    {/each}
  </div>
  <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Switching schemes preserves the extra header rows below.</p>
</div>

{#if form.auth_scheme === "bearer"}
  <div class="mt-4 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4">
    <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="cc-srv-bearer">
      Bearer token <span class="text-neg-400">*</span>
    </label>
    <input
      id="cc-srv-bearer"
      type="password"
      class={inputClass}
      placeholder="token"
      value={form.auth_secret}
      oninput={(e) => { form.auth_secret = (e.target as HTMLInputElement).value; onChange(); }}
    />
    <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Stored encrypted, decrypted server-side per request. Leave untouched when editing to keep the stored secret.</p>
  </div>
{:else if form.auth_scheme === "custom_header"}
  <div class="mt-4 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4">
    <span class="block text-xs font-medium text-black-800 dark:text-black-600">Auth headers</span>
    <div class="mt-2">
      <McpHeadersEditor rows={form.auth_headers} onChange={(rows) => { form.auth_headers = rows; onChange(); }} defaultSecret={true} />
    </div>
    <p class="mt-2 text-[11px] text-black-700 dark:text-black-600">Paired ID + secret headers, subscription keys. Rows marked secret encrypt at rest.</p>
  </div>
{:else if form.auth_scheme === "oauth"}
  <div class="mt-4 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4">
    <p class="text-xs text-black-800 dark:text-black-600">
      Standard MCP authorization. Each instance connects its own account — Test now opens the login; saving creates the connector with the signed-in account.
    </p>
    <div class="mt-3 grid grid-cols-12 gap-3">
      <div class="col-span-12 sm:col-span-5">
        <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="cc-srv-oauth-id">Client ID (optional)</label>
        <input
          id="cc-srv-oauth-id"
          type="text"
          class={inputClass}
          placeholder="auto via dynamic registration"
          value={form.oauth.client_id ?? ""}
          oninput={(e) => { form.oauth.client_id = (e.target as HTMLInputElement).value; onChange(); }}
        />
      </div>
      <div class="col-span-12 sm:col-span-4">
        <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="cc-srv-oauth-secret">Client secret (optional)</label>
        <input
          id="cc-srv-oauth-secret"
          type="password"
          class={inputClass}
          placeholder="public client when empty"
          value={form.oauth.client_secret ?? ""}
          oninput={(e) => { form.oauth.client_secret = (e.target as HTMLInputElement).value; onChange(); }}
        />
      </div>
      <div class="col-span-12 sm:col-span-3">
        <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="cc-srv-oauth-scopes">Scopes (optional)</label>
        <input
          id="cc-srv-oauth-scopes"
          type="text"
          class={inputClass}
          placeholder="space-separated"
          value={form.oauth.scopes ?? ""}
          oninput={(e) => { form.oauth.scopes = (e.target as HTMLInputElement).value; onChange(); }}
        />
      </div>
    </div>
    {#if oauthConnected}
      <div class="mt-3 rounded-lg border border-pos-400 bg-pos-100 px-3 py-2" data-cc-oauth-status>
        <p class="text-[11px] text-black-800"><span class="font-semibold text-pos-400">✓ Signed in.</span> Saving attaches this account to the connector's first instance.</p>
      </div>
    {/if}
  </div>
{:else if form.auth_scheme === "sso"}
  <div class="mt-4 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4">
    <p class="text-xs text-black-800 dark:text-black-600">
      Wick mints a short-lived ED25519-signed JWT for the calling user and forwards it as <code class="font-mono">X-Wick-User</code>. No shared secret is stored.
    </p>
    <span class="mt-3 block text-xs font-medium text-black-800 dark:text-black-600">Claim mapping (read-only)</span>
    <pre class="mt-1 overflow-auto rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-3 font-mono text-xs leading-relaxed text-black-900 dark:text-white-100">{ssoClaimExample}</pre>
    <div class="mt-3 grid grid-cols-12 gap-3">
      <div class="col-span-12 sm:col-span-7">
        <span class="block text-xs font-medium text-black-800 dark:text-black-600">Audience (<code class="font-mono">aud</code> claim)</span>
        <div class="mt-1">
          <TextInput
            value={form.sso.audience ?? ""}
            onChange={(v) => { form.sso.audience = v; onChange(); }}
            placeholder="defaults to the MCP URL host"
            ariaLabel="SSO audience"
          />
        </div>
        <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">The MCP server should validate this — prevents token re-use across servers.</p>
      </div>
      <div class="col-span-12 sm:col-span-5">
        <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="cc-srv-sso-ttl">Token TTL</label>
        <select
          id="cc-srv-sso-ttl"
          class="mt-1 w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-2 text-sm text-black-900 dark:text-white-100 outline-none focus:border-green-500"
          value={form.sso.ttl_seconds ?? 300}
          onchange={(e) => { form.sso.ttl_seconds = parseInt((e.target as HTMLSelectElement).value, 10) || 300; onChange(); }}
        >
          {#each ttlOptions as o (o.value)}
            <option value={o.value}>{o.label}</option>
          {/each}
        </select>
        <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Re-minted per request — short TTL is safe.</p>
      </div>
    </div>
  </div>
  <div class="mt-3 rounded-lg border border-pos-400 bg-pos-100 px-3 py-2">
    <p class="text-[11px] text-black-800"><span class="font-semibold text-pos-400">✓ Why SSO:</span> no shared secret in wick; per-user RBAC + audit on the MCP side; revoking a wick user revokes downstream access instantly.</p>
  </div>
  <div class="mt-2 rounded-lg border border-cau-400 bg-cau-100 px-3 py-2">
    <p class="text-[11px] text-black-800"><span class="font-semibold text-cau-400">⚠ Server requirement:</span> the MCP server must validate the JWT against <code class="font-mono">/.well-known/wick-pubkey.pem</code> on this wick. Stock open-source MCP servers don't — typically only in-house ones.</p>
  </div>
{/if}
