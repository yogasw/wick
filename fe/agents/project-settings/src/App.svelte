<script lang="ts">
  import { ToastHost } from "@wick-fe/common-ui";
  import ProjectSettingsForm from "$lib/components/ProjectSettingsForm.svelte";

  function getProjectID(): string {
    return document.getElementById("app")?.dataset.projectId ?? "";
  }

  function getBase(): string {
    return document.getElementById("app")?.dataset.base ?? "";
  }
</script>

<div class="min-h-screen p-6">
  <ToastHost />
  <ProjectSettingsForm projectID={getProjectID()} base={getBase()} />
</div>
