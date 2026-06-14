<script lang="ts">
  import Composer from "../../Composer.svelte";

  type Props = { onSend: (msg: { text: string; files: File[] }) => void };
  let { onSend }: Props = $props();
</script>

{#snippet leading()}
  <span data-leading-action>LEADING</span>
{/snippet}

<Composer {onSend} leadingActions={leading} />
