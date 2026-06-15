<script lang="ts">
  /* Generic confirm dialog for destructive actions (Delete workflow, Delete
     node, Discard draft, Unpublish). Built on the shared Modal shell + Button
     so every blocking confirmation looks the same — title + body + two buttons
     (Cancel + Confirm). destructive=true swaps the confirm button to red. */
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";

  type Props = {
    open: boolean;
    title: string;
    body?: string;
    confirmLabel?: string;
    cancelLabel?: string;
    destructive?: boolean;
    onConfirm: () => void;
    onCancel: () => void;
  };

  let {
    open,
    title,
    body = "",
    confirmLabel = "Confirm",
    cancelLabel = "Cancel",
    destructive = false,
    onConfirm,
    onCancel,
  }: Props = $props();
</script>

<Modal {open} {title} onClose={onCancel} size="sm">
  {#if body}
    <p class="text-sm text-black-600 dark:text-black-600">{body}</p>
  {/if}
  {#snippet footer()}
    <Button variant="secondary" onclick={onCancel}>{cancelLabel}</Button>
    <Button variant={destructive ? "danger" : "primary"} onclick={onConfirm}>{confirmLabel}</Button>
  {/snippet}
</Modal>
