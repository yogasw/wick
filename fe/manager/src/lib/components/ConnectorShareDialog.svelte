<script lang="ts">
  /* Share dialog for a connector TYPE: who may know this connector exists.
     The owner (creator of a custom connector) shares by email and can hide it
     again; admins can also share with a tag (team) or open it to everyone.
     Rules live server-side (connectors/type_share.go); this only drives them. */
  import { Button, Modal, Select, TextInput, Toggle } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import { getConnectorSharing, setConnectorSharing } from "$lib/api.js";
  import type { ConnectorSharing, ShareAction } from "$lib/types.js";

  type Props = {
    open: boolean;
    connectorKey: string;
    connectorName: string;
    onClose: () => void;
    /* Called with the new visibility after every change. */
    onChange?: (visibility: "everyone" | "restricted") => void;
  };
  let { open, connectorKey, connectorName, onClose, onChange }: Props = $props();

  let data = $state<ConnectorSharing | null>(null);
  let loading = $state(false);
  let busy = $state(false);
  let error = $state("");
  let email = $state("");
  let tagPick = $state("");

  let restricted = $derived(data?.visibility === "restricted");
  let tagOptions = $derived(
    (data?.available_tags ?? [])
      .filter((t) => !(data?.tags ?? []).some((x) => x.id === t.id))
      .map((t) => ({ value: t.id, label: t.name })),
  );

  async function load() {
    loading = true;
    error = "";
    try {
      data = await getConnectorSharing(connectorKey);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    if (open) {
      email = "";
      tagPick = "";
      load();
    }
  });

  async function run(req: ShareAction): Promise<boolean> {
    if (busy) return false;
    busy = true;
    error = "";
    try {
      data = await setConnectorSharing(connectorKey, req);
      onChange?.(data.visibility);
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      toastError("Sharing not updated", error);
      return false;
    } finally {
      busy = false;
    }
  }

  async function addEmail() {
    const v = email.trim();
    if (!v) return;
    if (await run({ action: "share_email", email: v })) email = "";
  }

  async function addTag() {
    if (!tagPick) return;
    if (await run({ action: "link_tag", tag_id: tagPick })) tagPick = "";
  }

  function onEmailKey(e: KeyboardEvent) {
    if (e.key === "Enter") {
      e.preventDefault();
      addEmail();
    }
  }
</script>

<Modal {open} title="Share {connectorName}" size="md" {onClose}>
  {#if loading && !data}
    <p class="py-6 text-center text-sm text-black-700 dark:text-black-600">Loading…</p>
  {:else if data}
    <div class="space-y-5" data-share-dialog>
      <!-- Visibility -->
      <div class="flex items-start justify-between gap-4 rounded-lg border border-white-400 dark:border-navy-600 px-4 py-3">
        <div class="min-w-0">
          <p class="text-sm font-medium text-black-900 dark:text-white-100">
            {restricted ? "Hidden" : "Visible to everyone"}
          </p>
          <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
            {#if restricted}
              Only the people and tags below{data.owner ? ", the owner" : ""} and admins know this connector exists.
            {:else}
              Every user sees this connector and can create their own instance.
            {/if}
          </p>
        </div>
        {#if data.is_admin}
          <span class="flex flex-shrink-0 items-center gap-2 text-xs text-black-800 dark:text-black-600">
            Everyone
            <Toggle
              checked={!restricted}
              disabled={busy}
              label="Visible to everyone"
              onChange={(v) => run({ action: v ? "everyone" : "restrict" })}
            />
          </span>
        {:else if !restricted}
          <Button size="sm" variant="secondary" disabled={busy} onclick={() => run({ action: "restrict" })}>Hide</Button>
        {/if}
      </div>

      {#if restricted}
        <!-- People -->
        <section class="space-y-2">
          <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">People</h3>
          <div class="flex gap-2">
            <div class="min-w-0 flex-1" role="presentation" onkeydown={onEmailKey}>
              <TextInput
                id="share-email"
                type="email"
                value={email}
                onChange={(v) => (email = v)}
                placeholder="name@company.com"
                ariaLabel="Email to share with"
                disabled={busy}
              />
            </div>
            <Button size="md" disabled={busy || !email.trim()} onclick={addEmail}>Share</Button>
          </div>
          <ul class="divide-y divide-white-300 dark:divide-navy-600 rounded-lg border border-white-400 dark:border-navy-600">
            {#if data.owner}
              <li class="flex items-center justify-between gap-3 px-3 py-2 text-sm">
                <span class="min-w-0 truncate text-black-900 dark:text-white-100">
                  {data.owner.name || data.owner.email || data.owner.id}
                  {#if data.owner.email && data.owner.name}<span class="text-xs text-black-700 dark:text-black-600"> · {data.owner.email}</span>{/if}
                </span>
                <span class="flex-shrink-0 text-xs text-black-700 dark:text-black-600">Owner</span>
              </li>
            {/if}
            {#each data.people as p (p.id)}
              <li class="flex items-center justify-between gap-3 px-3 py-2 text-sm">
                <span class="min-w-0 truncate text-black-900 dark:text-white-100">
                  {p.name || p.email || p.id}
                  {#if p.email && p.name}<span class="text-xs text-black-700 dark:text-black-600"> · {p.email}</span>{/if}
                </span>
                <Button size="sm" variant="ghost" disabled={busy} onclick={() => run({ action: "unshare_user", user_id: p.id })}>Remove</Button>
              </li>
            {/each}
            {#if !data.owner && data.people.length === 0}
              <li class="px-3 py-2 text-xs text-black-700 dark:text-black-600">Not shared with anyone yet.</li>
            {/if}
          </ul>
        </section>

        <!-- Tags: admins manage, owners only see what an admin linked -->
        {#if data.is_admin || data.tags.length > 0}
          <section class="space-y-2">
            <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Tags</h3>
            {#if data.is_admin}
              <div class="flex gap-2">
                <div class="min-w-0 flex-1">
                  <Select
                    value={tagPick}
                    options={tagOptions}
                    onChange={(v) => (tagPick = v)}
                    placeholder={tagOptions.length ? "Pick a tag…" : "No more tags"}
                    disabled={busy || tagOptions.length === 0}
                  />
                </div>
                <Button size="md" disabled={busy || !tagPick} onclick={addTag}>Add</Button>
              </div>
            {:else}
              <p class="text-xs text-black-700 dark:text-black-600">Added by an admin. Everyone holding these tags can see this connector.</p>
            {/if}
            {#if data.tags.length > 0}
              <div class="flex flex-wrap gap-2">
                {#each data.tags as t (t.id)}
                  <span class="inline-flex items-center gap-1 rounded-full bg-white-300 dark:bg-navy-600 px-2.5 py-0.5 text-xs font-medium text-black-800 dark:text-black-600">
                    {t.name}
                    {#if data.is_admin}
                      <button
                        type="button"
                        class="ml-0.5 text-black-700 hover:text-neg-400 disabled:opacity-50"
                        aria-label="Remove tag {t.name}"
                        disabled={busy}
                        onclick={() => run({ action: "unlink_tag", tag_id: t.id })}
                      >×</button>
                    {/if}
                  </span>
                {/each}
              </div>
            {/if}
          </section>
        {/if}
      {/if}

      {#if error}
        <p class="rounded-lg border border-neg-400 bg-neg-100 px-3 py-2 text-xs text-black-800" data-share-error>{error}</p>
      {/if}
    </div>
  {:else if error}
    <p class="rounded-lg border border-neg-400 bg-neg-100 px-3 py-2 text-xs text-black-800">{error}</p>
  {/if}
  {#snippet footer()}
    <div class="flex justify-end">
      <Button variant="secondary" size="lg" onclick={onClose}>Done</Button>
    </div>
  {/snippet}
</Modal>
