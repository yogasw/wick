<script lang="ts">
  import type { WsInstance } from "../types/agents.js";

  type TestResult = { ok: boolean; error?: string; no_health_check?: boolean } | null;

  type Props = {
    instance: WsInstance;
    open?: boolean;
    onSave: (cid: string, values: Record<string, string>) => void;
    onTest: (cid: string, config: Record<string, string>) => Promise<TestResult>;
    onRename: (cid: string, label: string) => void;
    onDuplicate: (cid: string) => void;
    onDelete: (cid: string) => void;
  };

  let { instance, open = false, onSave, onTest, onRename, onDuplicate, onDelete }: Props = $props();

  let isOpen = $state(false);
  let renaming = $state(false);
  let renameValue = $state("");
  let testResult: TestResult = $state(null);
  let testing = $state(false);
  let saveError = $state("");

  /* initialized from prop on mount — secrets always blank, non-secrets pre-fill */
  let fieldValues: Record<string, string> = $state({});
  let origValues: Record<string, string> = $state({});

  $effect(() => {
    isOpen = open;
    renameValue = instance.label ?? "";
    const initial: Record<string, string> = {};
    for (const f of instance.fields ?? []) {
      /* secrets load blank — stored value is never echoed back to the UI */
      initial[f.key] = f.secret ? "" : (f.value ?? "");
    }
    fieldValues = { ...initial };
    origValues = { ...initial };
  });

  const dirtyFields = $derived(
    Object.keys(fieldValues).filter((k) => fieldValues[k] !== origValues[k]),
  );

  $effect(() => {
    void JSON.stringify(fieldValues);
    saveError = "";
  });

  const anyDirty = $derived(dirtyFields.length > 0);

  function dirtyValues(): Record<string, string> {
    const out: Record<string, string> = {};
    for (const k of dirtyFields) {
      out[k] = fieldValues[k].trim();
    }
    return out;
  }

  function handleSave() {
    const values = dirtyValues();
    if (!Object.keys(values).length) return;
    saveError = "";
    try {
      onSave(instance.id, values);
    } catch (e: unknown) {
      saveError = e instanceof Error ? e.message : String(e);
    }
  }

  async function handleTest() {
    testing = true;
    testResult = null;
    try {
      testResult = await onTest(instance.id, dirtyValues());
    } finally {
      testing = false;
    }
  }

  function handleReset() {
    for (const k of Object.keys(fieldValues)) {
      fieldValues[k] = origValues[k];
    }
    testResult = null;
  }

  function commitRename() {
    const next = renameValue.trim();
    if (next && next !== (instance.label ?? "")) {
      onRename(instance.id, next);
    }
    renaming = false;
  }

  function cancelRename() {
    renameValue = instance.label ?? "";
    renaming = false;
  }

  function statusBadgeCls(status: string): string {
    return status === "ready"
      ? "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300"
      : "bg-cau-100 text-cau-700 dark:bg-cau-900 dark:text-cau-300";
  }

  function testResultCls(result: TestResult): string {
    if (!result) return "";
    return result.ok ? "text-green-600 dark:text-green-400" : "text-neg-400";
  }

  function testResultText(result: TestResult): string {
    if (!result) return "";
    if (result.ok) return "Looks good.";
    if (result.no_health_check) return "No health check for this connector — run an operation to verify.";
    return result.error ?? "Test failed.";
  }

  const INPUT_CLASS =
    "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none";
</script>

<div class="rounded-xl border border-white-300 dark:border-navy-600 overflow-hidden" data-cid={instance.id}>
  <!-- Header -->
  <button
    type="button"
    class="w-full flex items-center gap-2 px-3 py-2 bg-white-200 dark:bg-navy-800 text-left hover:bg-white-300 dark:hover:bg-navy-700 transition-colors"
    onclick={() => { if (!renaming) isOpen = !isOpen; }}
  >
    <span
      class="shrink-0 text-black-600 dark:text-black-700 transition-transform"
      style:transform={isOpen ? "rotate(90deg)" : ""}
    >
      <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path>
      </svg>
    </span>

    <span class="flex-1 min-w-0 flex items-center gap-1.5">
      {#if renaming}
        <input
          type="text"
          bind:value={renameValue}
          class="flex-1 min-w-0 rounded-md border border-green-500 bg-white-100 dark:bg-navy-800 px-2 py-0.5 text-sm font-medium text-black-900 dark:text-white-100 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none"
          onclick={(e) => e.stopPropagation()}
          onkeydown={(e) => {
            if (e.key === "Enter") { e.preventDefault(); commitRename(); }
            else if (e.key === "Escape") { e.preventDefault(); cancelRename(); }
          }}
          onblur={commitRename}
          data-testid="rename-input"
        />
      {:else}
        <span class="min-w-0 truncate text-sm font-medium text-black-900 dark:text-white-100">
          {instance.label ?? instance.id}
        </span>
        <span
          class="shrink-0 text-black-600 dark:text-black-700 hover:text-green-600 dark:hover:text-green-400 transition-colors cursor-pointer"
          title="Rename"
          role="button"
          tabindex="0"
          onclick={(e) => { e.stopPropagation(); renaming = true; renameValue = instance.label ?? ""; }}
          onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") { e.stopPropagation(); renaming = true; } }}
        >
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M11.5 2.5l2 2L6 12l-2.5.5.5-2.5 7.5-7.5z" stroke-linecap="round" stroke-linejoin="round"></path>
          </svg>
        </span>
      {/if}
    </span>

    <span class={"shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium " + statusBadgeCls(instance.status)}>
      {instance.status === "ready" ? "ready" : "needs setup"}
    </span>
  </button>

  <!-- Body -->
  {#if isOpen}
    <div class="p-3 space-y-3" data-testid="card-body">
      {#each (instance.fields ?? []) as field (field.key)}
        {@const fieldId = `ws-${instance.id}-${field.key}`}
        <div>
          <label
            for={fieldId}
            class="block text-xs font-medium text-black-800 dark:text-white-200 mb-1"
          >
            {field.label ?? field.key}
            {#if field.required}
              <span class="ml-1 text-neg-400">*</span>
            {/if}
            {#if field.set}
              <span class="ml-1 text-[10px] text-green-600 dark:text-green-400">• set</span>
            {/if}
          </label>

          {#if field.type === "dropdown"}
            <select
              id={fieldId}
              class={INPUT_CLASS}
              bind:value={fieldValues[field.key]}
              data-field={field.key}
            >
              {#each (field.options ?? []) as opt (opt)}
                <option value={opt}>{opt}</option>
              {/each}
            </select>
          {:else}
            <input
              id={fieldId}
              type={field.secret ? "password" : "text"}
              class={INPUT_CLASS}
              bind:value={fieldValues[field.key]}
              placeholder={field.secret
                ? (field.set ? "•••• set" : (field.placeholder ?? "Enter to set"))
                : (field.placeholder ?? "")}
              autocomplete={field.secret ? "new-password" : undefined}
              data-field={field.key}
              data-secret={field.secret ? "true" : undefined}
            />
          {/if}

          {#if field.help}
            <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">{field.help}</p>
          {/if}
        </div>
      {/each}

      {#if testResult !== null}
        <p class={"text-xs font-medium " + testResultCls(testResult)} data-testid="test-result">
          {testResultText(testResult)}
        </p>
      {/if}

      {#if saveError}
        <p class="text-xs font-medium text-neg-400" data-testid="save-error">{saveError}</p>
      {/if}

      <!-- Actions -->
      <div class="flex flex-wrap items-center gap-2 pt-1">
        <button
          type="button"
          class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors disabled:opacity-50"
          disabled={!anyDirty}
          onclick={handleSave}
        >Save</button>

        {#if anyDirty}
          <button
            type="button"
            class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-sm font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
            onclick={handleReset}
          >Reset</button>
        {/if}

        <button
          type="button"
          class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-sm font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors disabled:opacity-50"
          disabled={testing}
          onclick={handleTest}
        >{testing ? "Testing…" : "Test"}</button>

        <button
          type="button"
          class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-sm font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
          onclick={() => onDuplicate(instance.id)}
        >Duplicate</button>

        <button
          type="button"
          class="ml-auto text-[11px] font-medium text-neg-400 hover:underline"
          onclick={() => onDelete(instance.id)}
        >Remove</button>
      </div>
    </div>
  {/if}
</div>
