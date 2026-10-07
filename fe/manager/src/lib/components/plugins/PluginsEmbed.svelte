<script lang="ts">
  /* Root for /admin/plugins: the plugins view plus the toast host AppShell
     would otherwise supply. The admin layout owns the page chrome. */
  import { ToastHost } from "@wick-fe/common-ui";
  import PluginsAdmin from "./PluginsAdmin.svelte";
</script>

<ToastHost />
<PluginsAdmin />
