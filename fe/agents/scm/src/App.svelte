<script lang="ts">
  import ScmPanel from "$lib/components/ScmPanel.svelte";

  // Props from the island host (session page). Standalone hits read the
  // session from ?session= inside the store, so all props are optional.
  let {
    sessionID,
    mode = "full",
    pinned = false,
    onPinToggle,
    onClose,
  }: {
    sessionID?: string;
    mode?: "sidebar" | "full";
    pinned?: boolean;
    onPinToggle?: () => void;
    onClose?: () => void;
  } = $props();
</script>

<ScmPanel {sessionID} {mode} {pinned} {onPinToggle} {onClose} />
