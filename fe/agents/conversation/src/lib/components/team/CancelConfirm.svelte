<script lang="ts">
  /* The step before a team task is canceled: canceling stops the
     teammate's current turn, and on some providers whatever was typed
     into that turn meanwhile goes with it. */
  type Props = { name: string; onConfirm: () => void; onKeep: () => void };
  let { name, onConfirm, onKeep }: Props = $props();
</script>

<div data-testid="cancel-confirm" role="alertdialog" aria-label="Cancel this task?" class="flex flex-col gap-1.5 rounded-md border border-red-500/40 bg-red-500/5 px-2 py-1.5 text-[11px]">
  <span class="text-black-900 dark:text-white-100">Cancel this task? {name}'s current turn will be stopped, along with anything typed into it while it runs.</span>
  <span class="flex items-center gap-2">
    <button type="button" data-testid="cancel-confirm-yes" onclick={onConfirm} class="rounded-md bg-red-500 px-2 py-0.5 font-medium text-white-100 hover:bg-red-600">Yes, cancel</button>
    <button type="button" data-testid="cancel-confirm-keep" onclick={onKeep} class="text-black-700 dark:text-black-600 hover:underline">Keep working</button>
  </span>
</div>
