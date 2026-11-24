<script lang="ts">
  import type { ComponentProps } from "svelte";
  import AccountMenu from "../../../../../common/ui/src/AccountMenu.svelte";
  let props: ComponentProps<typeof AccountMenu> = $props();
</script>
<AccountMenu {...props}/>
