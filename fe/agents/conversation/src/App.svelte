<script lang="ts">
  const appEl = document.getElementById("app");
  const base = $derived(appEl?.dataset.base ?? "");
</script>

<div class="flex h-full flex-col items-center justify-center gap-2 text-black-900 dark:text-white-100">
  <h1 class="text-2xl font-bold">Conversation SPA</h1>
  <p class="text-sm text-black-600 dark:text-black-400">{base || "(no base set)"}</p>
</div>
