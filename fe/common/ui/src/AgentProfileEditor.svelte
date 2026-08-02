<script lang="ts">
  /* The one sub-agent role form, shared by the global Sub-agents page and
     the project-settings tab. It is written once on purpose: the form is
     fifteen fields, and a second copy would fall behind the first the
     moment a field is added.

     Scope-agnostic — the caller owns `profile.project_id` and decides
     which surface is showing. This component only edits what it is given. */
  import { untrack } from "svelte";
  import type { AgentProfile } from "@wick-fe/common-api";
  import { canLeadDelegation } from "@wick-fe/common-api";
  import Button from "./Button.svelte";
  import LabeledInput from "./LabeledInput.svelte";
  import NumberInput from "./NumberInput.svelte";
  import Select from "./Select.svelte";
  import TextArea from "./TextArea.svelte";
  import TextInput from "./TextInput.svelte";

  type TagOption = { id: string; name: string };

  type Props = {
    profile: AgentProfile;
    providers?: string[];
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
    providers = ["claude", "codex", "wick", "gemini"],
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
        disabled={readonly || !isNew}
        placeholder="code-reviewer"
      />
    </LabeledInput>

    <LabeledInput label="Name" helper="Shown in lists; defaults to the key">
      <TextInput
        value={draft.name}
        onChange={(v) => (draft.name = v)}
        disabled={readonly}
        placeholder="Code Reviewer"
      />
    </LabeledInput>
  </div>

  <LabeledInput label="Description" required error={descriptionError}>
    <TextArea
      value={draft.description}
      onChange={(v) => (draft.description = v)}
      rows={2}
      disabled={readonly}
      placeholder="Reviews a diff and returns findings ranked by severity."
    />
  </LabeledInput>

  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
    <LabeledInput label="Provider">
      <Select
        value={draft.provider}
        options={providers}
        onChange={(v) => (draft.provider = v)}
        disabled={readonly}
      />
    </LabeledInput>

    <LabeledInput label="Model" helper="Empty uses the provider default">
      <TextInput
        value={draft.model}
        onChange={(v) => (draft.model = v)}
        disabled={readonly}
        placeholder="default"
      />
    </LabeledInput>
  </div>

  <LabeledInput label="System prompt">
    <TextArea
      value={draft.system_prompt}
      onChange={(v) => (draft.system_prompt = v)}
      rows={6}
      disabled={readonly}
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
              disabled={readonly}
              onchange={() => toggleTag(t.id)}
            />
            {t.name}
          </label>
        {/each}
      </div>
    {/if}
  </LabeledInput>

  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
    <LabeledInput label="Max turns" helper="Clamped to the system ceiling">
      <NumberInput
        value={draft.default_max_turns}
        onChange={(v) => (draft.default_max_turns = v)}
        min={1}
        disabled={readonly}
      />
    </LabeledInput>

    <LabeledInput
      label="Allowed native tools"
      helper="Comma-separated. Empty uses the provider's default set."
    >
      <TextInput
        value={nativeToolsText}
        onChange={setNativeTools}
        disabled={readonly}
        placeholder="Read, Grep, WebSearch"
      />
    </LabeledInput>
  </div>

  <div class="flex flex-col gap-2">
    <label class="flex items-start gap-2 text-xs text-black-800 dark:text-white-100">
      <input
        type="checkbox"
        class="mt-0.5 accent-green-500"
        bind:checked={draft.strict_mcp}
        disabled={readonly}
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
        disabled={readonly || !leaderCapable}
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
        disabled={readonly}
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
        disabled={readonly}
      />
      <span>
        Disabled
        <span class="block text-[11px] text-black-700 dark:text-black-600">
          Kept on record but hidden from every roster.
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
      {#if ondelete && !isNew}
        <Button variant="danger" class="ml-auto" onclick={() => ondelete?.(draft)}>
          Delete
        </Button>
      {/if}
    </div>
  {/if}
</form>
