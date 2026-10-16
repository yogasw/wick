<script lang="ts">
  /* TerminalModal — the gotty web terminal of one provider instance in a
     modal iframe. Reusable outside the Detail page: start a session with
     apiTerminalStart(base, type, name, commandKey), pass it here, and the
     modal kills it when closed (then calls onClose). Admin-only
     server-side. */
  import { Modal } from "@wick-fe/common-ui";
  import { apiTerminalClose, type TerminalSession } from "$lib/terminal.js";

  type Props = {
    base: string;
    type: string;
    name: string;
    session: TerminalSession | null;
    onClose: () => void;
  };
  let { base, type, name, session, onClose }: Props = $props();

  function close() {
    const s = session;
    onClose();
    if (s) void apiTerminalClose(base, type, name, s.id).catch(() => {});
  }
</script>

<Modal open={session !== null} title={session ? session.command : ""} size="2xl" closeOnBackdrop={false} onClose={close}>
  {#if session}
    <div data-testid="terminal-modal" class="h-[70vh]">
      <iframe title="terminal" src={session.url} class="h-full w-full rounded-lg border border-white-300 dark:border-navy-600 bg-black"></iframe>
    </div>
  {/if}
</Modal>
