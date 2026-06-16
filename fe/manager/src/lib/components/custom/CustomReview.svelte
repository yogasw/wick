<script lang="ts">
  /* The review / edit page: a sticky toolbar (Save, plus enable-disable and
     delete in edit mode) over the shared DraftEditor. New drafts arrive via
     the sessionStorage hand-off the paste/manual flows write; edit drafts
     load from the JSON draft endpoint. Save posts to the create or update
     endpoint; create redirects to the connector page, update reloads the
     live module in place. Mirrors custom_review.templ + the save/delete
     half of custom_review.js. */
  import { Button, ConfirmDialog } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { push } from "$lib/router.js";
  import {
    getCustomMeta,
    getCustomDraft,
    saveCustomDraft,
    updateCustomDraft,
    deleteCustomDef,
    setCustomDefDisabled,
  } from "$lib/api.js";
  import { normalize, serialize } from "./draft.js";
  import { DRAFT_STORAGE_KEY } from "./storage.js";
  import DraftEditor from "./DraftEditor.svelte";
  import type { Draft } from "$lib/types.js";

  type Props = { defID?: string };
  let { defID = "" }: Props = $props();

  let editMode = $derived(!!defID);

  let draft = $state<Draft | null>(null);
  let categories = $state<string[]>([]);
  let loading = $state(true);
  let error = $state("");
  let disabled = $state(false);
  let saving = $state(false);
  let saved = $state(false);
  let confirmDelete = $state(false);
  const appBase = document.getElementById("app")?.dataset.base ?? "";

  async function load() {
    loading = true;
    error = "";
    try {
      const meta = await getCustomMeta();
      categories = meta.categories;
      if (editMode) {
        const res = await getCustomDraft(defID);
        if (res.mcp) {
          if (!res.server_id) {
            error = "Couldn't resolve the MCP server for this connector definition.";
            return;
          }
          push(`/custom/mcp/${encodeURIComponent(res.server_id)}/edit`);
          return;
        }
        disabled = res.disabled;
        draft = normalize(res.draft);
      } else {
        const stored = sessionStorage.getItem(DRAFT_STORAGE_KEY);
        draft = normalize(stored ? (JSON.parse(stored) as Partial<Draft>) : {});
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  function onChange() {
    saved = false;
  }

  async function save() {
    if (!draft || saving) return;
    saving = true;
    error = "";
    try {
      const payload = serialize(draft);
      if (editMode) {
        const res = await updateCustomDraft(defID, payload);
        if (res.reload_error) {
          error = `Saved, but live reload failed: ${res.reload_error} — open the connector to retry.`;
        } else {
          saved = true;
          toastOk("Changes saved");
        }
      } else {
        const res = await saveCustomDraft(payload);
        sessionStorage.removeItem(DRAFT_STORAGE_KEY);
        toastOk("Connector saved");
        if (res.redirect) {
          window.location.href = res.redirect;
        }
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      toastError("Save failed", error);
    } finally {
      saving = false;
    }
  }

  async function toggleDisabled() {
    try {
      disabled = await setCustomDefDisabled(defID, !disabled);
      toastOk(disabled ? "Connector disabled" : "Connector enabled");
    } catch (e) {
      toastError("Action failed", e instanceof Error ? e.message : String(e));
    }
  }

  async function doDelete() {
    confirmDelete = false;
    try {
      await deleteCustomDef(defID);
      toastOk("Connector deleted");
      push("/");
    } catch (e) {
      toastError("Delete failed", e instanceof Error ? e.message : String(e));
    }
  }

  $effect(() => { load(); });
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if error && !draft}
  <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
{:else if draft}
  <div class="space-y-4">
    <div class="sticky top-16 z-30 -mx-6 border-b border-white-300 bg-white-200 px-6 py-3 dark:border-navy-600 dark:bg-navy-800">
      <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
        <h1 class="min-w-0 truncate text-lg font-semibold text-black-900 dark:text-white-100">
          {editMode ? "Edit connector definition" : "Review extracted definition"}
        </h1>
        <div class="flex flex-wrap items-center gap-2 sm:flex-shrink-0">
          {#if editMode}
            <Button variant="secondary" onclick={toggleDisabled}>{disabled ? "Enable" : "Disable"}</Button>
            <Button variant="danger" onclick={() => (confirmDelete = true)}>Delete</Button>
          {/if}
          <Button variant="primary" size="lg" disabled={saving} onclick={save}>
            {#if saving}Saving…{:else if saved}Saved ✓{:else if editMode}Save changes{:else}Save connector →{/if}
          </Button>
        </div>
      </div>
    </div>

    {#if error}
      <div class="rounded-lg border border-neg-400 bg-neg-100 px-4 py-3 text-sm font-medium text-neg-400">✗ {error}</div>
    {/if}

    <DraftEditor {draft} {categories} {editMode} {onChange} />
  </div>

  <ConfirmDialog
    open={confirmDelete}
    title="Delete this custom connector?"
    body="Deletes the definition and its instances. This cannot be undone."
    confirmLabel="Delete"
    destructive
    onConfirm={doDelete}
    onCancel={() => (confirmDelete = false)}
  />
{/if}
