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
  import type { BreadcrumbItem } from "@wick-fe/common-ui";

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

  const home: BreadcrumbItem = { label: "Connectors", onClick: () => push("/") };

  let items = $derived.by<BreadcrumbItem[]>(() => {
    if (auditRoute) {
      return [{ label: "Audit Log" }];
    }
    if (jobParams) {
      return [
        { label: "Jobs" },
        { label: jobName, onClick: () => push(`/jobs/${encodeURIComponent(jobParams.key)}`), truncate: true },
      ];
    }
    if (toolParams) {
      return [
        { label: "Tools" },
        { label: toolName, onClick: () => push(`/tools/${encodeURIComponent(toolParams.key)}`), truncate: true },
      ];
    }
    if (customCrumb) {
      return [home, { label: customCrumb }];
    }
    if (listParams) {
      return [home, { label: connectorName }];
    }
    if (rowCrumb) {
      const trail: BreadcrumbItem[] = [
        home,
        { label: connectorName, onClick: () => push(`/connectors/${encodeURIComponent(rowCrumb.key)}`), truncate: true },
      ];
      if (testParams || historyParams) {
        trail.push({
          label: rowName,
          onClick: () => push(`/connectors/${encodeURIComponent(rowCrumb.key)}/${encodeURIComponent(rowCrumb.id)}`),
          truncate: true,
        });
        trail.push({ label: testParams ? "Test" : "History" });
      } else {
        trail.push({ label: rowName });
      }
      return trail;
    }
    // Connectors index: no breadcrumb — the page already shows a
    // "Connectors" heading, so a single-item crumb would just repeat it.
    return [];
  });
</script>

<AppShell {items}>
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
