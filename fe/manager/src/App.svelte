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
  import { breadcrumbNames } from "$lib/stores/breadcrumb.js";

  let currentRoute = $state(get(route));
  $effect(() => {
    const unsub = route.subscribe((r) => { currentRoute = r; });
    return unsub;
  });

  let names = $state(get(breadcrumbNames));
  $effect(() => {
    const unsub = breadcrumbNames.subscribe((n) => { names = n; });
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

  /* Display names come from the page's loaded data (published into the
     breadcrumb store). Fall back to the raw URL key until the fetch lands so
     the trail is never blank. */
  let connectorName = $derived(names.connector ?? listParams?.key ?? rowCrumb?.key ?? "");
  let rowName = $derived(names.row ?? rowCrumb?.id ?? "");
  let jobName = $derived(names.job ?? jobParams?.key ?? "");
  let toolName = $derived(names.tool ?? toolParams?.key ?? "");
</script>

<AppShell>
  {#snippet sep()}
    <span aria-hidden="true">/</span>
  {/snippet}
  {#snippet homeLink()}
    <button type="button" class="whitespace-nowrap hover:text-green-600" onclick={() => push("/")}>Home</button>
  {/snippet}
  {#snippet current(label: string)}
    <span class="inline-block max-w-[55vw] truncate align-bottom text-black-900 dark:text-white-100 sm:max-w-[18rem]">{label}</span>
  {/snippet}
  {#snippet breadcrumb()}
    {#if auditRoute}
      {@render homeLink()}
      {@render sep()}
      {@render current("Audit Log")}
    {:else if jobParams}
      <span>Jobs</span>
      {@render sep()}
      <button type="button" class="inline-block max-w-[55vw] truncate align-bottom text-black-900 dark:text-white-100 hover:text-green-600 sm:max-w-[18rem]" onclick={() => push(`/jobs/${encodeURIComponent(jobParams.key)}`)}>{jobName}</button>
    {:else if toolParams}
      <span>Tools</span>
      {@render sep()}
      <button type="button" class="inline-block max-w-[55vw] truncate align-bottom text-black-900 dark:text-white-100 hover:text-green-600 sm:max-w-[18rem]" onclick={() => push(`/tools/${encodeURIComponent(toolParams.key)}`)}>{toolName}</button>
    {:else if customCrumb}
      {@render homeLink()}
      {@render sep()}
      <button type="button" class="hover:text-green-600" onclick={() => push("/")}>Connectors</button>
      {@render sep()}
      {@render current(customCrumb)}
    {:else if listParams}
      {@render homeLink()}
      {@render sep()}
      {@render current(connectorName)}
    {:else if rowCrumb}
      {@render homeLink()}
      {@render sep()}
      <button type="button" class="inline-block max-w-[55vw] truncate align-bottom hover:text-green-600 sm:max-w-[18rem]" onclick={() => push(`/connectors/${encodeURIComponent(rowCrumb.key)}`)}>{connectorName}</button>
      {@render sep()}
      {#if testParams || historyParams}
        <button type="button" class="inline-block max-w-[55vw] truncate align-bottom hover:text-green-600 sm:max-w-[18rem]" onclick={() => push(`/connectors/${encodeURIComponent(rowCrumb.key)}/${encodeURIComponent(rowCrumb.id)}`)}>{rowName}</button>
        {@render sep()}
        {@render current(testParams ? "Test" : "History")}
      {:else}
        {@render current(rowName)}
      {/if}
    {:else}
      {@render homeLink()}
      {@render sep()}
      {@render current("Connectors")}
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
