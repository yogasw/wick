<script lang="ts">
  import { get } from "svelte/store";
  import { route, match, push } from "$lib/router.js";
  import AppShell from "$lib/components/AppShell.svelte";
  import ConnectorsIndex from "$lib/components/ConnectorsIndex.svelte";
  import ConnectorList from "$lib/components/ConnectorList.svelte";
  import ConnectorDetail from "$lib/components/ConnectorDetail.svelte";
  import ConnectorTest from "$lib/components/ConnectorTest.svelte";
  import ConnectorHistory from "$lib/components/ConnectorHistory.svelte";
  import CustomPaste from "$lib/components/custom/CustomPaste.svelte";
  import CustomManual from "$lib/components/custom/CustomManual.svelte";
  import CustomReview from "$lib/components/custom/CustomReview.svelte";
  import McpServerForm from "$lib/components/custom/McpServerForm.svelte";
  import JobDetail from "$lib/components/jobs/JobDetail.svelte";
  import ToolDetail from "$lib/components/tools/ToolDetail.svelte";
  import AuditLog from "$lib/components/audit/AuditLog.svelte";

  let currentRoute = $state(get(route));
  $effect(() => {
    const unsub = route.subscribe((r) => { currentRoute = r; });
    return unsub;
  });

  let pasteRoute = $derived(currentRoute === "/custom/paste");
  let manualRoute = $derived(currentRoute === "/custom/manual");
  let reviewRoute = $derived(currentRoute === "/custom/review");
  let mcpNewRoute = $derived(currentRoute === "/custom/mcp");
  let auditRoute = $derived(currentRoute === "/audit");
  let mcpEditParams = $derived(match("/custom/mcp/:serverID/edit", currentRoute));
  let editParams = $derived(match("/custom/:defID/edit", currentRoute));
  let jobParams = $derived(match("/jobs/:key", currentRoute));
  let toolParams = $derived(match("/tools/:key", currentRoute));
  let testParams = $derived(match("/connectors/:key/:id/test", currentRoute));
  let historyParams = $derived(match("/connectors/:key/:id/history", currentRoute));
  let detailParams = $derived(match("/connectors/:key/:id", currentRoute));
  let listParams = $derived(match("/connectors/:key", currentRoute));

  let section = $derived.by(() => {
    if (auditRoute) return "audit";
    if (jobParams) return "jobs";
    if (toolParams) return "tools";
    return "connectors";
  });

  let rowCrumb = $derived(testParams ?? historyParams ?? detailParams);
  let customCrumb = $derived.by(() => {
    if (pasteRoute) return "From paste";
    if (manualRoute) return "Manual builder";
    if (reviewRoute) return "Review";
    if (mcpNewRoute) return "Register MCP server";
    if (mcpEditParams) return "Edit MCP server";
    if (editParams) return "Edit definition";
    return "";
  });
</script>

<AppShell {section}>
  {#snippet breadcrumb()}
    {#if auditRoute}
      <span class="text-black-900 dark:text-white-100">Audit Log</span>
    {:else if jobParams}
      <span class="text-black-700 dark:text-black-600">Jobs</span>
      <span aria-hidden="true"> / </span>
      <span class="text-black-900 dark:text-white-100">{jobParams.key}</span>
    {:else if toolParams}
      <span class="text-black-700 dark:text-black-600">Tools</span>
      <span aria-hidden="true"> / </span>
      <span class="text-black-900 dark:text-white-100">{toolParams.key}</span>
    {:else}
      <button type="button" class="hover:text-green-600" onclick={() => push("/")}>Connectors</button>
      {#if customCrumb}
        <span aria-hidden="true"> / </span>
        <span class="text-black-900 dark:text-white-100">{customCrumb}</span>
      {:else if listParams}
        <span aria-hidden="true"> / </span>
        <span class="text-black-900 dark:text-white-100">{listParams.key}</span>
      {:else if rowCrumb}
        <span aria-hidden="true"> / </span>
        <button type="button" class="hover:text-green-600" onclick={() => push(`/connectors/${encodeURIComponent(rowCrumb.key)}`)}>{rowCrumb.key}</button>
        <span aria-hidden="true"> / </span>
        {#if testParams || historyParams}
          <button type="button" class="hover:text-green-600" onclick={() => push(`/connectors/${encodeURIComponent(rowCrumb.key)}/${encodeURIComponent(rowCrumb.id)}`)}>{rowCrumb.id}</button>
          <span aria-hidden="true"> / </span>
          <span class="text-black-900 dark:text-white-100">{testParams ? "Test" : "History"}</span>
        {:else}
          <span class="text-black-900 dark:text-white-100">{rowCrumb.id}</span>
        {/if}
      {/if}
    {/if}
  {/snippet}
  {#key currentRoute}
    {#if auditRoute}
      <AuditLog />
    {:else if jobParams}
      <JobDetail jobKey={jobParams.key} />
    {:else if toolParams}
      <ToolDetail toolKey={toolParams.key} />
    {:else if pasteRoute}
      <CustomPaste />
    {:else if manualRoute}
      <CustomManual />
    {:else if reviewRoute}
      <CustomReview />
    {:else if mcpNewRoute}
      <McpServerForm />
    {:else if mcpEditParams}
      <McpServerForm serverId={mcpEditParams.serverID} />
    {:else if editParams}
      <CustomReview defID={editParams.defID} />
    {:else if testParams}
      <ConnectorTest connectorKey={testParams.key} connectorId={testParams.id} />
    {:else if historyParams}
      <ConnectorHistory connectorKey={historyParams.key} connectorId={historyParams.id} />
    {:else if detailParams}
      <ConnectorDetail connectorKey={detailParams.key} connectorId={detailParams.id} />
    {:else if listParams}
      <ConnectorList connectorKey={listParams.key} />
    {:else}
      <ConnectorsIndex />
    {/if}
  {/key}
</AppShell>
