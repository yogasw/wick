<script lang="ts">
  /* The one sub-agent role form, shared by the global Sub-agents page and
     the project-settings tab. It is written once on purpose: the form is
     fifteen fields, and a second copy would fall behind the first the
     moment a field is added.

     Scope-agnostic — the caller owns `profile.project_id` and decides
     which surface is showing. This component only edits what it is given. */
  import { untrack } from "svelte";
  import type { AgentProfile, ProviderListItem } from "@wick-fe/common-api";
  import { canLeadDelegation, normalizeProviderKey } from "@wick-fe/common-api";
  import Button from "./Button.svelte";
  import LabeledInput from "./LabeledInput.svelte";
  import NumberInput from "./NumberInput.svelte";
  import ProviderPicker from "./ProviderPicker.svelte";
  import TextArea from "./TextArea.svelte";
  import TextInput from "./TextInput.svelte";
  import { buildProviderOptions } from "./provider-options.js";

  type TagOption = { id: string; name: string };

  type Props = {
    profile: AgentProfile;
    /** Provider instances this role may run on, with their models. */
    providerList?: ProviderListItem[];
    tags?: TagOption[];
    /** Read-only mode: every control disabled, Save and Delete hidden.
        How a non-admin sees a global role. */
    readonly?: boolean;
    saving?: boolean;
    /** Server-side error for the whole form (e.g. a rejected save). */
    error?: string;
    onsave: (p: AgentProfile) => void;
    ondelete?: (p: AgentProfile) => void;
    oncancel?: () => void;
  };

  let {
    profile,
    providerList = [],
    tags = [],
    readonly = false,
    saving = false,
    error = "",
    onsave,
    ondelete,
    oncancel,
  }: Props = $props();

  // Local working copy so an abandoned edit does not mutate the list
  // behind the form. The snapshot is deliberate — untrack says so, rather
  // than leaving it to look like a missed reactive dependency.
  const stampOf = (p: AgentProfile) => `${p.project_id}/${p.id}/${p.key}`;
  let draft = $state<AgentProfile>(untrack(() => ({ ...profile })));
  let touched = $state(false);
  let lastStamp = untrack(() => stampOf(profile));

  // Re-seed only when the caller swaps to a DIFFERENT profile. Keyed on
  // scope+id+key rather than object identity, because a parent that
  // rebuilds its list on every render would otherwise wipe the form
  // mid-edit.
  $effect(() => {
    const stamp = stampOf(profile);
    untrack(() => {
      if (stamp === lastStamp) return;
      lastStamp = stamp;
      draft = { ...profile };
      touched = false;
    });
  });

  const isNew = $derived(draft.id === "");
  const leaderCapable = $derived(canLeadDelegation(draft.provider));

  // Locked freezes the role; readonly is how a non-admin sees a global
  // role. Both disable the form, but only readonly hides the way out —
  // the Locked checkbox stays live under a lock, or the lock would have
  // no key.
  const frozen = $derived(readonly || draft.locked);

  // Provider and model are two columns but one choice. The picker speaks
  // the composer's packed form ("type/name::modelID"); the form splits it
  // back apart on the way to the server, so nothing downstream changes.
  // normalizeProviderKey is what keeps a role stored before instances
  // existed ("claude") pointing at a real option ("claude/claude").
  const providerKey = $derived(draft.provider ? normalizeProviderKey(draft.provider) : "");
  const pickerValue = $derived(draft.model ? `${providerKey}::${draft.model}` : providerKey);
  const providerOptions = $derived(buildProviderOptions(providerList, pickerValue));

  function pickProvider(v: string) {
    const i = v.indexOf("::");
    draft.provider = i < 0 ? v : v.slice(0, i);
    draft.model = i < 0 ? "" : v.slice(i + 2);
  }

  // The description is what a leader model reads to decide whether this
  // role is the right one to hand work to, so the server refuses an empty
  // one. Surface that on the field rather than as a toast after the fact.
  const descriptionError = $derived(
    touched && draft.description.trim() === ""
      ? "Required — the delegating agent reads this to decide when to pick this role"
      : "",
  );
  const keyError = $derived(touched && draft.key.trim() === "" ? "Required" : "");
  const valid = $derived(draft.key.trim() !== "" && draft.description.trim() !== "");
  // Save stays clickable while the form is incomplete on purpose. A
  // disabled submit button explains nothing — pressing it is how the user
  // finds out WHICH field is missing, so validation is reported on submit
  // rather than pre-empted by a dead control.
  const canSubmit = $derived(!readonly && !saving);

  const nativeToolsText = $derived(draft.allowed_native_tools.join(", "));

  function setNativeTools(v: string) {
    draft.allowed_native_tools = v
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
  }

  function toggleTag(id: string) {
    draft.allowed_tag_ids = draft.allowed_tag_ids.includes(id)
      ? draft.allowed_tag_ids.filter((t) => t !== id)
      : [...draft.allowed_tag_ids, id];
  }

  function submit() {
    touched = true;
    if (!canSubmit || !valid) return;
    // The server forces can_delegate off for providers that cannot use
    // MCP tools. Mirror it here so what is sent matches what is stored.
    onsave({ ...draft, can_delegate: leaderCapable && draft.can_delegate });
  }
</script>

<form
  class="flex flex-col gap-4"
  onsubmit={(e) => {
    e.preventDefault();
    submit();
  }}
>
  {#if error}
    <p
      class="rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-xs text-rose-700 dark:border-rose-900 dark:bg-rose-950 dark:text-rose-300"
    >
      {error}
    </p>
  {/if}

  {#if draft.locked && !readonly}
    <p
      class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-200"
    >
      Locked — untick Locked and save to edit this role. Its fields and the
      Delete button stay frozen until you do.
    </p>
  {/if}

  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
    <LabeledInput
      label="Key"
      required
      error={keyError}
      helper="Stable handle the agent passes to wick_delegate, e.g. code-reviewer"
    >
      <TextInput
        value={draft.key}
        onChange={(v) => (draft.key = v)}
        disabled={frozen || !isNew}
        placeholder="code-reviewer"
      />
    </LabeledInput>

    <LabeledInput label="Name" helper="Shown in lists; defaults to the key">
      <TextInput
        value={draft.name}
        onChange={(v) => (draft.name = v)}
        disabled={frozen}
        placeholder="Code Reviewer"
      />
    </LabeledInput>
  </div>

  <LabeledInput label="Description" required error={descriptionError}>
    <TextArea
      value={draft.description}
      onChange={(v) => (draft.description = v)}
      rows={2}
      disabled={frozen}
      placeholder="Reviews a diff and returns findings ranked by severity."
    />
  </LabeledInput>

  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
    <LabeledInput
      label="Provider"
      helper="Pick the instance, and its model where the provider offers a choice"
    >
      <!-- ProviderPicker has no disabled prop: a read-only or frozen form
           blocks it from the outside instead of growing one. -->
      {#if frozen}
        <div class="pointer-events-none opacity-60">
          <ProviderPicker
            options={providerOptions}
            value={pickerValue}
            onChange={pickProvider}
            placeholder="Select provider"
          />
        </div>
      {:else}
        <ProviderPicker
          options={providerOptions}
          value={pickerValue}
          onChange={pickProvider}
          placeholder="Select provider"
        />
      {/if}
    </LabeledInput>

    <LabeledInput label="Max turns" helper="Clamped to the system ceiling">
      <NumberInput
        value={draft.default_max_turns}
        onChange={(v) => (draft.default_max_turns = v)}
        min={1}
        disabled={frozen}
      />
    </LabeledInput>
  </div>

  <LabeledInput label="System prompt">
    <TextArea
      value={draft.system_prompt}
      onChange={(v) => (draft.system_prompt = v)}
      rows={6}
      disabled={frozen}
      placeholder="You review code and return findings ranked by severity."
    />
  </LabeledInput>

  <LabeledInput
    label="Allowed tags"
    helper="Empty inherits the caller's tags in full. Listing tags only NARROWS — a role can never grant access the person delegating does not already have."
  >
    {#if tags.length === 0}
      <p class="text-xs text-black-700 dark:text-black-600">No tags defined.</p>
    {:else}
      <div class="flex flex-wrap gap-2">
        {#each tags as t (t.id)}
          <label
            class="flex cursor-pointer items-center gap-1.5 rounded-lg border border-white-400 px-2 py-1 text-xs text-black-800 transition-colors hover:bg-white-200 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-700"
          >
            <input
              type="checkbox"
              class="accent-green-500"
              checked={draft.allowed_tag_ids.includes(t.id)}
              disabled={frozen}
              onchange={() => toggleTag(t.id)}
            />
            {t.name}
          </label>
        {/each}
      </div>
    {/if}
  </LabeledInput>

  <LabeledInput
    label="Allowed native tools"
    helper="Comma-separated. Empty uses the provider's default set."
  >
    <TextInput
      value={nativeToolsText}
      onChange={setNativeTools}
      disabled={frozen}
      placeholder="Read, Grep, WebSearch"
    />
  </LabeledInput>

  <div class="flex flex-col gap-2">
    <label class="flex items-start gap-2 text-xs text-black-800 dark:text-white-100">
      <input
        type="checkbox"
        class="mt-0.5 accent-green-500"
        bind:checked={draft.strict_mcp}
        disabled={frozen}
      />
      <span>
        Strict MCP
        <span class="block text-[11px] text-black-700 dark:text-black-600">
          Drops the host's own MCP servers from this role's spawn. Turning it
          off lets the sub-agent reach tools that never pass wick's tag filter.
        </span>
      </span>
    </label>

    <label class="flex items-start gap-2 text-xs text-black-800 dark:text-white-100">
      <input
        type="checkbox"
        class="mt-0.5 accent-green-500"
        bind:checked={draft.can_delegate}
        disabled={frozen || !leaderCapable}
      />
      <span>
        Can delegate
        <span class="block text-[11px] text-black-700 dark:text-black-600">
          {#if leaderCapable}
            Lets this role delegate onward. Still bounded by the depth limit.
          {:else}
            Unavailable — {draft.provider} is not verified for MCP tool use, so it cannot lead.
          {/if}
        </span>
      </span>
    </label>

    <label class="flex items-start gap-2 text-xs text-black-800 dark:text-white-100">
      <input
        type="checkbox"
        class="mt-0.5 accent-green-500"
        bind:checked={draft.allow_take_over}
        disabled={frozen}
      />
      <span>
        Allow take-over
        <span class="block text-[11px] text-black-700 dark:text-black-600">
          Lets a human steer this role's running sub-agents. Steered results
          are flagged, since they are no longer the role's unaided work.
        </span>
      </span>
    </label>

    <label class="flex items-start gap-2 text-xs text-black-800 dark:text-white-100">
      <input
        type="checkbox"
        class="mt-0.5 accent-green-500"
        bind:checked={draft.disabled}
        disabled={frozen}
      />
      <span>
        Disabled
        <span class="block text-[11px] text-black-700 dark:text-black-600">
          Kept on record but hidden from every roster.
        </span>
      </span>
    </label>

    <!-- Deliberately keyed on `readonly`, not `frozen`: this is the one
         control a locked role leaves alive, or the lock would have no key. -->
    <label class="flex items-start gap-2 text-xs text-black-800 dark:text-white-100">
      <input
        type="checkbox"
        class="mt-0.5 accent-green-500"
        bind:checked={draft.locked}
        disabled={readonly}
      />
      <span>
        Locked
        <span class="block text-[11px] text-black-700 dark:text-black-600">
          Freezes this role: no edit and no delete, from this page or from an
          agent over MCP. Untick and save to change it again.
        </span>
      </span>
    </label>
  </div>

  {#if !readonly}
    <div class="flex items-center gap-2 pt-2">
      <Button type="submit" disabled={!canSubmit}>
        {saving ? "Saving…" : "Save"}
      </Button>
      {#if oncancel}
        <Button variant="secondary" onclick={oncancel}>Cancel</Button>
      {/if}
      {#if ondelete && !isNew && !draft.locked}
        <Button variant="danger" class="ml-auto" onclick={() => ondelete?.(draft)}>
          Delete
        </Button>
      {/if}
    </div>
  {/if}
</form>
