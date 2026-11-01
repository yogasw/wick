<script lang="ts">
  /* The one "Delete project…" confirmation, shared by project settings and
     the project menu so both say the same thing and hit the same API. It
     asks the server what would go (chat count, custom folder) when opened.
     Delete unlocks only when the name is typed, the consent box is ticked
     and both sums are right (delete-project.ts) — fresh sums on every open. */
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";
  import {
    canConfirmDelete,
    DELETE_CONSENT,
    deleteProjectBody,
    deleteProjectTitle,
    makeSums,
    type DeleteProjectPreview,
    type Sum,
  } from "./delete-project.js";

  type Props = {
    open: boolean;
    base: string;
    projectID: string;
    onDeleted: () => void;
    onCancel: () => void;
    /* The sums' randomness; tests pass a fixed sequence. */
    rng?: () => number;
  };

  let { open, base, projectID, onDeleted, onCancel, rng = Math.random }: Props = $props();

  let preview = $state<DeleteProjectPreview | null>(null);
  let typed = $state("");
  let consent = $state(false);
  let sums = $state<[Sum, Sum]>([{ a: 2, b: 2 }, { a: 3, b: 3 }]);
  let answers = $state<[string, string]>(["", ""]);
  const ready = $derived(!!preview && canConfirmDelete(preview, { typed, consent, sums, answers }));
  let busy = $state(false);
  let error = $state("");

  const url = $derived(`${base}/projects/${encodeURIComponent(projectID)}`);

  $effect(() => {
    if (!open) return;
    preview = null;
    typed = "";
    consent = false;
    sums = makeSums(rng);
    answers = ["", ""];
    error = "";
    const target = `${url}/delete-preview`;
    fetch(target, { credentials: "same-origin" })
      .then(async (r) => {
        if (!r.ok) throw new Error((await r.text().catch(() => "")) || `HTTP ${r.status}`);
        preview = (await r.json()) as DeleteProjectPreview;
      })
      .catch((e: unknown) => { error = e instanceof Error ? e.message : String(e); });
  });

  async function confirm() {
    if (!ready || busy) return;
    busy = true;
    error = "";
    try {
      const r = await fetch(url, { method: "DELETE", credentials: "same-origin" });
      if (!r.ok) {
        let msg = await r.text().catch(() => "");
        try { msg = (JSON.parse(msg) as { error?: string }).error || msg; } catch { /* plain text */ }
        throw new Error(msg || `HTTP ${r.status}`);
      }
      onDeleted();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }
</script>

<Modal {open} title={preview ? deleteProjectTitle(preview) : "Delete project?"} onClose={onCancel} size="sm">
  {#if preview}
    {#if preview.protected}
      <p class="text-sm text-black-700 dark:text-black-600">This project can't be deleted (default/personal).</p>
    {:else}
      <p class="text-sm text-black-600 dark:text-black-600" data-testid="delete-project-body">{deleteProjectBody(preview)}</p>
      <label class="mt-3 block text-xs text-black-700 dark:text-black-600">
        Type <span class="font-semibold text-black-900 dark:text-white-100">{preview.name}</span> to confirm
        <input
          type="text"
          bind:value={typed}
          aria-label="Project name"
          autocomplete="off"
          class="mt-1 w-full rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-sm text-black-900 outline-none focus:border-neg-400 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
        />
      </label>
      <label class="mt-3 flex items-start gap-2 text-xs text-black-700 dark:text-black-600">
        <input type="checkbox" bind:checked={consent} data-testid="delete-project-consent" class="mt-0.5" />
        <span>{DELETE_CONSENT}</span>
      </label>
      <div class="mt-3 flex gap-3">
        {#each sums as s, i (i)}
          <label class="block flex-1 text-xs text-black-700 dark:text-black-600">
            {s.a} × {s.b} = ?
            <input
              type="text"
              inputmode="numeric"
              bind:value={answers[i]}
              aria-label={`Answer ${i + 1}`}
              autocomplete="off"
              class="mt-1 w-full rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-sm text-black-900 outline-none focus:border-neg-400 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
            />
          </label>
        {/each}
      </div>
    {/if}
  {:else if !error}
    <p class="text-sm text-black-700 dark:text-black-600">Loading…</p>
  {/if}
  {#if error}
    <p class="mt-3 text-xs text-neg-400" role="alert">{error}</p>
  {/if}
  {#snippet footer()}
    <Button variant="secondary" onclick={onCancel}>Cancel</Button>
    <Button
      variant="danger"
      disabled={!ready || busy}
      onclick={confirm}
    >{busy ? "Deleting…" : "Delete project"}</Button>
  {/snippet}
</Modal>
