<script lang="ts">
  /* The ticket page's bottom composer (Agents › Projects and Team ›
     Tickets): provider / model / preset, `/` commands and `@` files of the
     project, and starting a NEW session — on the ticket when one is
     given. Creating then attaching is a two-step, done before the new
     session opens so its first spawn already has the ticket pointer. */
  import type { ProjectOption, ProviderOption, ComposerCommand } from "../types/agents.js";
  import { toastError } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { createSessionInProject, getPresetOptions, getProviderOptionModels } from "../api/options.js";
  import { searchProjectMentionPaths } from "../api/files.js";
  import { listComposerCommands } from "../api/composer.js";
  import { attachSession } from "../api/tickets.js";
  import { Composer } from "@wick-fe/common-ui";
  import { NOTIFY_KEY } from "../notify-pref.js";

  type Props = {
    base: string;
    project: ProjectOption;
    providers: ProviderOption[];
    /** The ticket the next message lands on; null = a plain session. */
    ticketId: string | null;
    /** The ticket's title (or id) for the placeholder and footer. */
    ticketLabel?: string;
    /** Team › Tickets: the session answers as the caller's Captain
        (resolved by the server), named here. */
    teamCaptain?: boolean;
    captainName?: string;
    /** "Start without a ticket"; absent = not offered. */
    onClearTicket?: () => void;
    /** The session exists (and is on the ticket). Absent = open its page. */
    onCreated?: (sessionId: string, url: string) => void;
  };
  let { base, project, providers, ticketId, ticketLabel = "", teamCaptain = false, captainName = "", onClearTicket, onCreated }: Props = $props();

  let selectedProvider = $state<string>("");

  // A named instance is "type/name"; a default collapses to the bare type.
  function providerKey(p: ProviderOption): string {
    return p.name && p.name !== p.type ? `${p.type}/${p.name}` : p.type;
  }

  // Resolve the project's configured default ("codex" or "codex/name") to a
  // provider actually present in the list: exact key first, then bare type.
  //
  // The project's model rides along as the composer's packed
  // "type/name::modelID" value, but ONLY on an exact instance match: the
  // fallbacks land on a different instance, and a model id resolves against
  // the registry of the instance it was chosen on.
  function defaultProviderKey(): string {
    if (providers.length === 0) return "";
    const want = (project.defaultProvider ?? "").trim();
    if (want) {
      const exact = providers.find((p) => providerKey(p) === want);
      if (exact) {
        const key = providerKey(exact);
        const model = (project.defaultModel ?? "").trim();
        return model ? `${key}::${model}` : key;
      }
      const byType = providers.find((p) => p.type === want.split("/")[0]);
      if (byType) return providerKey(byType);
    }
    return providerKey(providers[0]);
  }

  $effect(() => {
    if (!selectedProvider && providers.length > 0) selectedProvider = defaultProviderKey();
  });

  // Live model loader — same contract as the conversation composer: split the
  // "type/name" value and fetch that instance's current vendor models, falling
  // back (composer-side) to the static list on error.
  function loadProviderModels(optionValue: string, opts?: { entry?: string; refresh?: boolean }) {
    const slash = optionValue.indexOf("/");
    const type = slash < 0 ? optionValue : optionValue.slice(0, slash);
    const name = slash < 0 ? optionValue : optionValue.slice(slash + 1);
    return Effect.runPromise(getProviderOptionModels(base, type, name, opts).pipe(Effect.provide(WickClientLayer)));
  }

  const providerSelect = $derived({
    options: providers.map((p) => ({
      label: p.name && p.name !== p.type ? `${p.type} · ${p.name}` : p.type,
      value: providerKey(p),
      badge: p.usesAIRouter ? "AI Router" : undefined,
      // Carry the model list so a multi-model provider (wick) shows the
      // nested model picker here too — matching the conversation composer.
      models: p.models,
    })),
    value: selectedProvider,
    onChange: (v: string) => { selectedProvider = v; },
    loadModels: loadProviderModels,
    usageBase: base,
  });

  // Preset selector — same affordance as the new-session page, so an init
  // from a project landing can override the preset before the session
  // starts. "" = the default preset (or the project's configured default).
  let presets = $state<string[]>([]);
  let selectedPreset = $state<string>("");
  $effect(() => {
    Effect.runPromise(getPresetOptions(base).pipe(Effect.provide(WickClientLayer)))
      .then((names) => { presets = names; })
      .catch(() => { /* presets optional */ });
  });
  const presetSelect = $derived(
    presets.length > 0
      ? {
          options: [
            { label: "— preset (default) —", value: "" },
            ...presets.map((n) => ({ label: n, value: n })),
          ],
          value: selectedPreset,
          onChange: (v: string) => { selectedPreset = v; },
        }
      : undefined,
  );

  // `@` searches THIS project's folder; `/` shows skills only (pre-session).
  function searchMentionFiles(query: string): Promise<string[]> {
    return Effect.runPromise(searchProjectMentionPaths(base, project.id, query).pipe(Effect.provide(WickClientLayer)))
      .catch(() => [] as string[]);
  }
  let composerCommands = $state<ComposerCommand[]>([]);
  $effect(() => {
    const providerType = selectedProvider ? selectedProvider.split("/")[0] : "";
    Effect.runPromise(listComposerCommands(base, "new", providerType).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        // key: c.id — `value` is the skill's frontmatter name, which repeats
        // across providers' skills dirs and would abort the keyed each block.
        // This page is the worst case for it: with no provider selected yet
        // providerType is "", so the server returns every provider's copy.
        composerCommands = (res.commands ?? []).map((c) => ({
          key: c.id, value: c.insert ?? c.id, label: c.label, hint: c.hint, category: c.category,
        }));
      })
      .catch(() => { /* commands optional */ });
  });

  /* The placeholder sits on ONE line that cannot wrap, so a long ticket
     title is clipped there; the footer carries a longer cut and the full
     title on hover. */
  const label = $derived(ticketLabel || ticketId || "");
  const labelShort = $derived(label.length > 32 ? label.slice(0, 32).trimEnd() + "…" : label);
  const labelBanner = $derived(label.length > 60 ? label.slice(0, 60).trimEnd() + "…" : label);

  async function handleSend({ text, files }: { text: string; files: File[] }) {
    try {
      const url = await createSessionInProject(base, text, files, selectedProvider, project.id, selectedPreset, teamCaptain);
      const sessionId = url.split("/").pop()?.split("?")[0] ?? "";
      // Attach before navigating: the new session must already belong to
      // the ticket when it opens, or its first spawn would miss the
      // ticket pointer and its notes.
      if (ticketId && sessionId) {
        await Effect.runPromise(
          attachSession(base, ticketId, sessionId, project.id).pipe(Effect.provide(WickClientLayer)),
        ).catch(() => { /* the session is still usable unattached */ });
      }
      if (onCreated && sessionId) onCreated(sessionId, url);
      else window.location.href = url;
    } catch (err) {
      toastError(err instanceof Error ? err.message : "Failed to create session");
    }
  }
</script>

<!-- The floating composer: bottom-centre like a helpdesk's. Sticky, so a
     long page never scrolls it away. -->
<div class="sticky bottom-0 z-10 mx-auto w-full max-w-3xl pb-1" data-testid="ticket-composer">
  <div class="rounded-xl border border-white-300 bg-white-100 p-3 shadow-lg dark:border-navy-600 dark:bg-navy-700">
    <Composer
      onSend={handleSend}
      placeholder={ticketId
        ? `Ask anything on ${labelShort}…   / commands · @ files`
        : "Ask anything…   / commands · @ files"}
      notifyKey={NOTIFY_KEY}
      provider={providerSelect}
      preset={presetSelect}
      onSearchFiles={searchMentionFiles}
      commands={composerCommands}
    />
    <p class="mt-2 text-center text-[11px] text-black-600 dark:text-black-700">
      {#if ticketId}
        New session on <span
          class="font-medium text-green-600 dark:text-green-400"
          title={label}
          >{labelBanner}</span
        > — this chat joins the ticket and reads its notes.
        {#if teamCaptain}
          <span class="font-medium text-black-800 dark:text-black-600" data-testid="ticket-composer-responder">→ {captainName || "Captain"} (your Captain)</span>
        {/if}
        {#if onClearTicket}
          <button
            type="button"
            onclick={onClearTicket}
            class="underline transition-colors hover:text-black-800 dark:hover:text-black-600"
            >Start without a ticket</button
          >
        {/if}
      {:else}
        New session in <span class="font-medium text-black-800 dark:text-black-600">{project.name}</span>{#if project.defaultProvider} · defaults to <span class="font-mono">{project.defaultProvider}</span>{/if}. Pick provider / model / preset above to override.
      {/if}
    </p>
  </div>
</div>
