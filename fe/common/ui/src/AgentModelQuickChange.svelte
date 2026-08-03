<script lang="ts">
  /* Quick provider/model change for one sub-agent role.

     Exists because changing which model a role runs on is the most frequent
     edit by far, and routing it through the full profile editor means
     scrolling past a system prompt and a tag list to reach one picker.

     Provider and model are committed together as the picker's packed
     "type/name::modelID" value, split apart only on save — a role's model id
     resolves against the instance it was chosen on, so the two must never be
     saved from different levels. */
  import type { AgentProfile, ProviderListItem } from "@wick-fe/common-api";
  import { normalizeProviderKey } from "@wick-fe/common-api";
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";
  import ProviderPicker from "./ProviderPicker.svelte";
  import { buildProviderOptions } from "./provider-options.js";
  import type { ComposerModelOption } from "./composer-types.js";

  type Props = {
    profile: AgentProfile;
    providerList: ProviderListItem[];
    saving?: boolean;
    /** Receives the role with provider+model replaced. */
    onsave: (p: AgentProfile) => void;
    onclose: () => void;
    /** Lazy model loader, so a wick live set can be drilled to its leaf. */
    loadModels?: (
      optionValue: string,
      opts?: { entry?: string },
    ) => Promise<ComposerModelOption[]>;
  };
  let { profile, providerList, saving = false, onsave, onclose, loadModels }: Props = $props();

  // Seeded from the role's stored pair. A role saved before instances existed
  // holds a bare type, promoted here so it matches a real picker option
  // instead of rendering as "(unavailable)".
  const initialKey = profile.provider ? normalizeProviderKey(profile.provider.split("::")[0]) : "";
  let value = $state(
    initialKey && profile.model ? `${initialKey}::${profile.model}` : initialKey,
  );

  const options = $derived(buildProviderOptions(providerList, value));
  const providerKey = $derived(value.split("::")[0] ?? "");
  const modelID = $derived.by(() => {
    const i = value.indexOf("::");
    return i < 0 ? "" : value.slice(i + 2);
  });
  const changed = $derived(
    providerKey !== initialKey || modelID !== (profile.model ?? ""),
  );

  function save() {
    onsave({ ...profile, provider: providerKey, model: modelID });
  }
</script>

<Modal open title={`Provider / model — ${profile.name || profile.key}`} size="md" onClose={onclose}>
  <div class="space-y-4">
    <div>
      <span class="mb-1 block text-xs text-black-600 dark:text-black-700">Runs on</span>
      <ProviderPicker
        options={options}
        {value}
        onChange={(v) => (value = v)}
        {loadModels}
        placeholder="Select provider"
      />
      <p class="mt-2 text-xs text-black-700 dark:text-black-600">
        Leave the model unset to use whatever that provider picks by default. Everything
        else about this role is unchanged.
      </p>
    </div>
  </div>

  {#snippet footer()}
    <div class="flex justify-end gap-2">
      <Button variant="secondary" onclick={onclose}>Cancel</Button>
      <Button variant="primary" disabled={saving || !changed} onclick={save}>
        {saving ? "Saving…" : "Save"}
      </Button>
    </div>
  {/snippet}
</Modal>
