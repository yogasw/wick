<script lang="ts">
  /* Per-project HTML-artifact CSP override.

     ONE toggle decides the posture — Secure, Unsecure, or Custom. The
     per-directive controls exist, but they are behind Custom and stay out of
     the way otherwise: the common answers are "sealed" and "let it through",
     and making an operator assemble either one out of six separate fields
     invites a half-configured policy nobody can read at a glance.

     Custom detail is REMEMBERED while a preset is selected — flipping to
     Secure and back does not wipe what was set up. Nothing is enforced from
     it while a preset is active; the backend ignores those fields outright.

     Inherited hosts are shown read-only under Custom because a project's own
     hosts are APPENDED to the global list — without seeing both, an operator
     cannot tell what a widget here may actually reach. */
  import type { WidgetPolicy } from "../types.js";

  type Props = {
    /** The project's stored override. */
    policy: WidgetPolicy | undefined;
    /** The global policy this project falls back to. */
    inherited: WidgetPolicy | undefined;
    /** Raw textarea text for this project's extra hosts. */
    allowlistText: string;
    onChange: (next: { policy: WidgetPolicy; allowlistText: string }) => void;
  };
  let { policy, inherited, allowlistText, onChange }: Props = $props();

  const PRESETS = [
    {
      value: "secure",
      label: "Secure",
      icon: "🔒",
      blurb: "Sealed off. No embeds, images, media, scripts, or network calls, and links cannot open a tab.",
    },
    {
      value: "unsecure",
      label: "Unsecure",
      icon: "🌐",
      blurb:
        "Everything allowed, including scripts from any host. Such a script can read whatever the widget holds and send it anywhere — for projects you trust.",
    },
    {
      value: "custom",
      label: "Custom",
      icon: "⚙️",
      blurb: "Choose each permission yourself.",
    },
  ];

  const DIRECTIVES = [
    {
      key: "frame_src" as const,
      label: "Embedded frames",
      help: "Nested iframes — Google Maps, YouTube embeds.",
    },
    {
      key: "img_src" as const,
      label: "External images",
      help: "Inline data: images always work; this is only about images fetched from a host.",
    },
    {
      key: "media_src" as const,
      label: "External audio & video",
      help: "Inline data: media always works; this is only about media fetched from a host.",
    },
    {
      key: "connect_src" as const,
      label: "Network calls",
      help: "fetch, XHR, and WebSocket. Anything reachable here can also receive data from the widget.",
    },
    {
      key: "script_src" as const,
      label: "External scripts",
      help:
        "Scripts loaded from another host. A permitted script runs inside the widget and can read everything it holds, so pair this with a narrow allowlist. The widget's own inline scripts always run.",
    },
  ];

  const MODES = [
    { value: "block", label: "Blocked" },
    { value: "list", label: "Allowlist only" },
    { value: "all", label: "Any HTTPS host" },
  ];

  const override = $derived(policy?.override === true);
  const preset = $derived(normalizePreset(policy?.mode));
  const isCustom = $derived(preset === "custom");
  const inheritedHosts = $derived(inherited?.allowlist ?? []);
  /* Escaping the sandbox only means anything if a tab may open at all, so the
     escape control follows the popup one rather than standing alone. */
  const popupsOn = $derived(policy?.allow_popups === true);
  /* Only the directives on "list" consult the allowlist. Naming them lets the
     hint say which ones a host would actually affect, instead of implying the
     list does something where nothing reads it. */
  const listDirectives = $derived(
    DIRECTIVES.filter((d) => mode(d.key) === "list").map((d) => d.label.toLowerCase()),
  );

  function normalizePreset(v: string | undefined): string {
    return v === "unsecure" || v === "custom" ? v : "secure";
  }

  function mode(key: (typeof DIRECTIVES)[number]["key"]): string {
    const v = policy?.[key];
    return v === "list" || v === "all" ? v : "block";
  }

  function emit(patch: Partial<WidgetPolicy>, text = allowlistText) {
    onChange({ policy: { ...(policy ?? {}), ...patch }, allowlistText: text });
  }

  /** One-line statement of what a policy permits, for the inherited summary. */
  function summary(p: WidgetPolicy | undefined): string {
    const pre = normalizePreset(p?.mode);
    if (pre === "secure") return "sealed off";
    if (pre === "unsecure") return "everything allowed";
    const open = DIRECTIVES.filter((d) => {
      const v = p?.[d.key];
      return v === "list" || v === "all";
    });
    if (!open.length && !p?.allow_popups) return "custom — nothing allowed";
    const parts = open.map((d) => `${d.label.toLowerCase()}: ${p?.[d.key]}`);
    if (p?.allow_popups) parts.push("popups allowed");
    if (p?.allow_popups && p?.allow_popup_escape) parts.push("tabs unsandboxed");
    return `custom — ${parts.join(" · ")}`;
  }
</script>

<!-- No heading or outer border here: the enclosing settings section owns
     the title, description, and card chrome. -->
<div>
  <div class="space-y-3">
    <label class="flex cursor-pointer items-start gap-2 rounded-lg border border-white-300 bg-white-200 px-4 py-3 text-sm dark:border-navy-600 dark:bg-navy-800">
      <input
        type="checkbox"
        checked={override}
        onchange={(e) => emit({ override: (e.currentTarget as HTMLInputElement).checked })}
        class="mt-0.5 rounded text-green-500 focus:ring-green-500"
      />
      <span>
        <span class="font-medium text-black-900 dark:text-white-100">Override for this project</span>
        {#if !override}
          <span
            class="ml-1 rounded bg-white-300 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-black-800 dark:bg-navy-700 dark:text-black-600"
            >inherited</span
          >
        {/if}
        <span class="mt-0.5 block text-xs leading-relaxed text-black-700 dark:text-black-600">
          {#if override}
            This project uses its own mode. Global no longer applies to it, apart from the allowed
            hosts, which are added to yours under Custom.
          {:else}
            Currently following global — {summary(inherited)}.
          {/if}
        </span>
      </span>
    </label>

    {#if override}
      <div class="space-y-3 pt-3 border-t border-white-300 dark:border-navy-600">
        <!-- The one knob. -->
        <div role="radiogroup" aria-label="Widget mode" class="grid grid-cols-3 gap-1.5">
          {#each PRESETS as p (p.value)}
            <button
              type="button"
              role="radio"
              aria-checked={preset === p.value}
              onclick={() => emit({ mode: p.value })}
              class="rounded-md border px-2 py-2 text-xs font-semibold transition-colors {preset ===
              p.value
                ? 'border-green-500 bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-300'
                : 'border-white-400 dark:border-navy-600 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800'}"
            >
              <span aria-hidden="true">{p.icon}</span> {p.label}
            </button>
          {/each}
        </div>
        <p class="text-xs text-black-600 dark:text-black-700">
          {PRESETS.find((p) => p.value === preset)?.blurb}
        </p>

        {#if isCustom}
          <div class="space-y-3 pt-3 border-t border-white-300 dark:border-navy-600">
            {#each DIRECTIVES as d (d.key)}
              <div>
                <label
                  for={`ps-widget-${d.key}`}
                  class="block text-black-600 dark:text-black-700 text-xs mb-0.5">{d.label}</label
                >
                <select
                  id={`ps-widget-${d.key}`}
                  value={mode(d.key)}
                  onchange={(e) => emit({ [d.key]: (e.currentTarget as HTMLSelectElement).value })}
                  class="w-full rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
                >
                  {#each MODES as m (m.value)}
                    <option value={m.value}>{m.label}</option>
                  {/each}
                </select>
                <p class="mt-1 text-xs text-black-600 dark:text-black-700">{d.help}</p>
              </div>
            {/each}

            <label class="flex items-start gap-2 text-sm cursor-pointer">
              <input
                type="checkbox"
                checked={policy?.allow_popups === true}
                onchange={(e) => emit({ allow_popups: (e.currentTarget as HTMLInputElement).checked })}
                class="mt-0.5 rounded text-green-500 focus:ring-green-500"
              />
              <span>
                <span class="font-semibold text-black-900 dark:text-white-100"
                  >Links may open a new tab</span
                >
                <span class="block text-xs text-black-600 dark:text-black-700 mt-0.5">
                  Needed for <code class="font-mono">target="_blank"</code> links. A new tab is
                  outside this policy, so it can reach any host — the allowed-hosts list does not
                  limit it. The tab still opens sandboxed unless you also allow the setting below.
                </span>
              </span>
            </label>

            <label
              class="flex items-start gap-2 text-sm {popupsOn
                ? 'cursor-pointer'
                : 'cursor-not-allowed opacity-60'}"
            >
              <input
                type="checkbox"
                disabled={!popupsOn}
                checked={policy?.allow_popup_escape === true && popupsOn}
                onchange={(e) =>
                  emit({ allow_popup_escape: (e.currentTarget as HTMLInputElement).checked })}
                class="mt-0.5 rounded text-green-500 focus:ring-green-500"
              />
              <span>
                <span class="font-semibold text-black-900 dark:text-white-100"
                  >Opened tabs get a real origin</span
                >
                <span class="block text-xs text-black-600 dark:text-black-700 mt-0.5">
                  {#if popupsOn}
                    Without this a new tab stays sandboxed and the site sees
                    <code class="font-mono">Origin: null</code>, which makes many pages load broken —
                    their own requests fail their CORS check. Turning it on lifts this policy from
                    the opened tab entirely; the widget itself is unaffected.
                  {:else}
                    Available once links may open a new tab.
                  {/if}
                </span>
              </span>
            </label>

            <div>
              <label
                for="ps-widget-allowlist"
                class="block text-black-600 dark:text-black-700 text-xs mb-0.5">Allowed hosts</label
              >
              {#if inheritedHosts.length}
                <div
                  class="mb-1 rounded-md border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-2 py-1.5"
                >
                  <p class="text-[10px] font-bold uppercase text-black-600 dark:text-black-700">
                    From global — always included
                  </p>
                  <p class="font-mono text-xs text-black-900 dark:text-white-100 break-all">
                    {inheritedHosts.join(" ")}
                  </p>
                </div>
              {/if}
              <textarea
                id="ps-widget-allowlist"
                rows="3"
                value={allowlistText}
                oninput={(e) => emit({}, (e.currentTarget as HTMLTextAreaElement).value)}
                placeholder={"maps.google.com\n*.example.com"}
                class="w-full rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs font-mono text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
              ></textarea>
              <p class="mt-1 text-xs text-black-600 dark:text-black-700">
                One host per line. <code class="font-mono">https://</code> is assumed and
                <code class="font-mono">*.example.com</code> covers subdomains. Plain
                <code class="font-mono">http://</code> and paths are rejected on save.
                {#if listDirectives.length}
                  Applies to {listDirectives.join(", ")}.
                {:else}
                  No permission above is set to "Allowlist only" yet, so nothing reads this list.
                {/if}
              </p>
            </div>
          </div>
        {/if}
      </div>
    {/if}
  </div>
</div>
