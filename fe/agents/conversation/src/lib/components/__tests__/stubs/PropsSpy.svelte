<script lang="ts">
  /* Test stub: stands in for a child component and hands its props to the
     test through globalThis.__propsSpy. */
  let props: Record<string, unknown> = $props();
  (globalThis as unknown as { __propsSpy?: Record<string, unknown> }).__propsSpy = props;
</script>
