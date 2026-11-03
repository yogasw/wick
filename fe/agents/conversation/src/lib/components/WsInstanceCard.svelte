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
    /** Sets ("4h") or clears ("none") the expiry; omitted = no picker. */
    /** Sets the lifetime: a fixed one (expires_in), a longer idle window
        (idle_after), or neither for the default. Omitted = no picker. */
    onExpiry?: (cid: string, lifetime: { expires_in?: string; idle_after?: string }) => void;
  };

  let { instance, open = false, onSave, onTest, onRename, onDuplicate, onDelete, onExpiry }: Props = $props();

  /* Quick picks; anything else goes through the custom hours/minutes or
     "until" fields. The server caps a lifetime at 7 days. */
  const EXPIRY_QUICK = [1, 2, 3];
  const MAX_EXPIRY_MIN = 7 * 24 * 60;

  let customAmount = $state("");
  let idleAmount = $state("");
  let idleUnit = $state<"m" | "h" | "d">("d");
  let customUnit = $state<"m" | "h" | "d">("h");
  const UNIT_MIN = { m: 1, h: 60, d: 1440 } as const;
  let untilValue = $state("");
  let expiryError = $state("");

  /* Sends a lifetime in whole minutes, which the server parses as "<n>m". */
  function sendMinutes(kind: "expires_in" | "idle_after", mins: number) {
    expiryError = "";
    if (!onExpiry) return;
    if (!(mins >= 1) || mins > MAX_EXPIRY_MIN) {
      expiryError = "Pick between 1 minute and 7 days.";
      return;
    }
    onExpiry(instance.id, { [kind]: `${Math.round(mins)}m` });
  }

  const setExpiryMinutes = (mins: number) => sendMinutes("expires_in", mins);

  function applyIdle() {
    sendMinutes("idle_after", Number(idleAmount || 0) * UNIT_MIN[idleUnit]);
  }

  /* "1d" / "3h" / "45m" for an idle window in seconds. */
  function fmtWindow(sec: number): string {
    const mins = Math.round(sec / 60);
    if (mins % 1440 === 0) return `${mins / 1440}d`;
    if (mins % 60 === 0) return `${mins / 60}h`;
    return `${mins}m`;
  }

  function applyCustom() {
    setExpiryMinutes(Number(customAmount || 0) * UNIT_MIN[customUnit]);
  }

  function applyUntil() {
    if (!untilValue) return;
    setExpiryMinutes(Math.ceil((new Date(untilValue).getTime() - Date.now()) / 60000));
  }

  /* "3h 20m" / "2d 4h" until expires_at; "" when unpinned or already past. */
  function timeLeft(expiresAt?: string): string {
    if (!expiresAt) return "";
    const ms = new Date(expiresAt).getTime() - Date.now();
    if (!(ms > 0)) return "";
    const mins = Math.ceil(ms / 60000);
    const d = Math.floor(mins / 1440);
    const h = Math.floor((mins % 1440) / 60);
    const m = mins % 60;
    if (d > 0) return `${d}d ${h}h`;
    if (h > 0) return `${h}h ${m}m`;
    return `${m}m`;
  }

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
      ? "bg-green-50 text-green-700 dark:bg-green-900 dark:text-green-300"
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

    {#if !instance.expires_at && instance.idle_after_sec}
      <span
        class="shrink-0 rounded-full bg-white-300 dark:bg-navy-700 px-2 py-0.5 text-[10px] font-medium text-black-800 dark:text-white-200"
        title="Deleted once the session has been unused this long"
        data-testid="expiry-badge"
      >⏳ idle {fmtWindow(instance.idle_after_sec)}</span>
    {:else if timeLeft(instance.expires_at)}
      <span
        class="shrink-0 rounded-full bg-white-300 dark:bg-navy-700 px-2 py-0.5 text-[10px] font-medium text-black-800 dark:text-white-200"
        title={`Deleted at ${new Date(instance.expires_at ?? "").toLocaleString()}`}
        data-testid="expiry-badge"
      >⏳ {timeLeft(instance.expires_at)}</span>
    {/if}

    <span class={"shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium " + statusBadgeCls(instance.status)}>
      {instance.status === "ready" ? "ready" : "needs setup"}
    </span>
  </button>

  <!-- Body -->
  {#if isOpen}
    <div class="p-3 space-y-3" data-testid="card-body">
      {#each (instance.fields ?? []) as field, fi (field.key)}
        {@const fieldId = `ws-${instance.id}-${field.key}`}
        {#if field.group && field.group !== (instance.fields ?? [])[fi - 1]?.group}
          <p class="pt-1 text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600" data-testid="ws-field-group">{field.group}</p>
        {/if}
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
            {#if field.locked}
              <span class="ml-1 text-[10px] text-black-700 dark:text-black-600" title="Locked for the agent: only you can change it here; otherwise the default applies">🔒 AI can't change</span>
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

      {#if onExpiry}
        <div class="space-y-2 rounded-lg border border-white-300 dark:border-navy-600 p-2" data-testid="expiry-picker">
          <p class="text-[11px] text-black-700 dark:text-black-600">
            {instance.expires_at
              ? `Expires ${new Date(instance.expires_at).toLocaleString()}`
              : instance.idle_after_sec
                ? `Deleted once the session is unused for ${fmtWindow(instance.idle_after_sec)}`
                : "Deleted 10 minutes after the session goes idle. Keep it longer:"}
          </p>
          <div class="flex flex-wrap items-center gap-1.5">
            {#each EXPIRY_QUICK as h (h)}
              <button type="button" class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors" onclick={() => setExpiryMinutes(h * 60)} data-testid="expiry-quick">{h}h</button>
            {/each}
            <input type="number" min="1" placeholder="0" bind:value={customAmount} class="w-16 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none" data-testid="expiry-amount" />
            <select bind:value={customUnit} class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none" data-testid="expiry-unit">
              <option value="m">minutes</option>
              <option value="h">hours</option>
              <option value="d">days</option>
            </select>
            <button type="button" class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors" onclick={applyCustom} data-testid="expiry-custom-set">Set</button>
          </div>
          <div class="flex flex-wrap items-center gap-1.5">
            <span class="text-[11px] text-black-700 dark:text-black-600">until</span>
            <input type="datetime-local" bind:value={untilValue} class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none" data-testid="expiry-until" />
            <button type="button" class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors" onclick={applyUntil} data-testid="expiry-until-set">Set</button>
          </div>
          <div class="flex flex-wrap items-center gap-1.5">
            <span class="text-[11px] text-black-700 dark:text-black-600">or when unused for</span>
            <input type="number" min="1" placeholder="0" bind:value={idleAmount} class="w-16 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none" data-testid="expiry-idle-amount" />
            <select bind:value={idleUnit} class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none" data-testid="expiry-idle-unit">
              <option value="m">minutes</option>
              <option value="h">hours</option>
              <option value="d">days</option>
            </select>
            <button type="button" class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors" onclick={applyIdle} data-testid="expiry-idle-set">Set</button>
            {#if instance.expires_at || instance.idle_after_sec}
              <button type="button" class="ml-auto text-[11px] font-medium text-black-700 dark:text-black-600 hover:underline" onclick={() => onExpiry?.(instance.id, {})} data-testid="expiry-clear">Back to default</button>
            {/if}
          </div>
          {#if expiryError}
            <p class="text-xs font-medium text-neg-400" data-testid="expiry-error">{expiryError}</p>
          {/if}
        </div>
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
