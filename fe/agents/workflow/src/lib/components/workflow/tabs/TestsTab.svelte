<script lang="ts">
  // Test cases panel — lists `__tests__/*.json`, lets user add / edit /
  // delete / run cases. Mirrors v1's TestManager templ but with an
  // inline assertion builder instead of free-text JSON.
  import { draftWorkflow } from "$lib/stores/editor";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import {
    workflowAPI,
    type TestAssertion,
    type TestCase,
    type TestRunResult,
  } from "$lib/api/workflow";

  type Row = {
    name: string;
    assertions: number;
    last_result?: "pass" | "fail";
    last_failures?: string[];
    duration_ms?: number;
  };

  type Props = {
    cases: { name: string; assertions: number }[];
    running?: boolean;
    onRunAll?: () => void;
    onRunOne?: (name: string) => void;
  };
  let { cases, running = false, onRunAll, onRunOne }: Props = $props();

  let rows = $state<Row[]>([]);
  $effect(() => {
    const next = (cases ?? []).map((c) => ({
      name: c.name,
      assertions: c.assertions,
      last_result: rows.find((r) => r.name === c.name)?.last_result,
      last_failures: rows.find((r) => r.name === c.name)?.last_failures,
      duration_ms: rows.find((r) => r.name === c.name)?.duration_ms,
    }));
    rows = next;
  });

  // Modal state — null means closed. Editing an existing case loads its
  // body lazily; creating a new one starts blank.
  let modalCase = $state<TestCase | null>(null);
  let modalLoading = $state(false);
  let modalSaving = $state(false);
  let modalError = $state("");
  let editingExisting = $state(false);
  let inputJson = $state("{}");
  let inputErr = $state("");
  let runningRow = $state<string | null>(null);

  const OPERATORS = [
    "equals",
    "not_equals",
    "contains",
    "not_contains",
    "exists",
    "not_exists",
    "gt",
    "gte",
    "lt",
    "lte",
    "matches",
  ];

  function freshCase(): TestCase {
    return {
      name: "",
      input: { Event: {} },
      assertions: [{ subject: "", operator: "equals", value: "" }],
    };
  }

  function openCreate() {
    modalCase = freshCase();
    inputJson = "{}";
    inputErr = "";
    modalError = "";
    editingExisting = false;
  }

  async function openEdit(name: string) {
    const wf = $draftWorkflow;
    if (!wf) return;
    modalLoading = true;
    modalError = "";
    editingExisting = true;
    try {
      const tc = await workflowAPI.testGet(wf.id, name);
      modalCase = {
        name: tc.name || name,
        input: tc.input ?? { Event: {} },
        assertions: tc.assertions?.length
          ? tc.assertions
          : [{ subject: "", operator: "equals", value: "" }],
        expected_output: tc.expected_output,
      };
      inputJson = JSON.stringify(modalCase.input?.Event ?? {}, null, 2);
      inputErr = "";
    } catch (e: any) {
      modalError = e?.message ?? "load failed";
      modalCase = freshCase();
      modalCase.name = name;
    } finally {
      modalLoading = false;
    }
  }

  function closeModal() {
    modalCase = null;
    modalError = "";
    inputErr = "";
  }

  function addAssertion() {
    if (!modalCase) return;
    modalCase = {
      ...modalCase,
      assertions: [
        ...modalCase.assertions,
        { subject: "", operator: "equals", value: "" },
      ],
    };
  }

  function removeAssertion(idx: number) {
    if (!modalCase) return;
    modalCase = {
      ...modalCase,
      assertions: modalCase.assertions.filter((_, i) => i !== idx),
    };
  }

  function operatorTakesValue(op: string): boolean {
    return op !== "exists" && op !== "not_exists";
  }

  async function saveCase() {
    if (!modalCase) return;
    const wf = $draftWorkflow;
    if (!wf) return;
    const name = (modalCase.name ?? "").trim();
    if (!name) {
      modalError = "name required";
      return;
    }
    if (!/^[A-Za-z0-9_-]+$/.test(name)) {
      modalError = "name must be slug-safe (a-z, 0-9, dash, underscore)";
      return;
    }
    // Parse the event JSON. Empty → {} ; anything else must be a valid
    // JSON object so the engine can hydrate it into workflow.Event.
    let evt: Record<string, unknown> = {};
    const trimmed = inputJson.trim();
    if (trimmed) {
      try {
        const v = JSON.parse(trimmed);
        if (v === null || typeof v !== "object" || Array.isArray(v)) {
          inputErr = "event must be a JSON object";
          return;
        }
        evt = v as Record<string, unknown>;
        inputErr = "";
      } catch (e: any) {
        inputErr = "invalid JSON: " + (e?.message ?? e);
        return;
      }
    }
    // Strip empty rows so the saved fixture stays tidy.
    const assertions = modalCase.assertions
      .filter((a) => (a.subject ?? "").trim() !== "")
      .map((a) => ({
        subject: a.subject.trim(),
        operator: a.operator,
        value: operatorTakesValue(a.operator) ? coerceValue(a.value) : undefined,
      }));
    modalSaving = true;
    modalError = "";
    try {
      await workflowAPI.testSave(wf.id, {
        name,
        input: { Event: evt, Node: modalCase.input?.Node },
        assertions,
      });
      toastOk(editingExisting ? "Test case updated" : "Test case added");
      closeModal();
      await refresh();
    } catch (e: any) {
      modalError = e?.message ?? "save failed";
    } finally {
      modalSaving = false;
    }
  }

  // Asserted value comes from a text input; coerce to number/bool/null
  // when the string parses cleanly so the engine compares like-for-like.
  // (raw equality of "42" against integer 42 would fail otherwise.)
  function coerceValue(v: unknown): unknown {
    if (typeof v !== "string") return v;
    const s = v.trim();
    if (s === "") return "";
    if (s === "true") return true;
    if (s === "false") return false;
    if (s === "null") return null;
    if (/^-?\d+$/.test(s)) return Number(s);
    if (/^-?\d*\.\d+$/.test(s)) return Number(s);
    if ((s.startsWith("{") && s.endsWith("}")) || (s.startsWith("[") && s.endsWith("]"))) {
      try { return JSON.parse(s); } catch { /* fall through */ }
    }
    return s;
  }

  async function refresh() {
    onRunAll?.();
  }

  async function runRow(name: string) {
    const wf = $draftWorkflow;
    if (!wf) return;
    runningRow = name;
    try {
      const res: TestRunResult = await workflowAPI.testRun(wf.id, name);
      rows = rows.map((r) =>
        r.name === name
          ? {
              ...r,
              last_result: res.pass ? "pass" : "fail",
              last_failures: res.failures ?? [],
              duration_ms: res.duration_ms,
            }
          : r,
      );
      if (res.pass) toastOk(`✓ ${name} passed (${res.duration_ms}ms)`);
      else toastError(`✕ ${name} failed (${res.failures?.length ?? 0})`);
    } catch (e: any) {
      toastError(e?.message ?? "run failed");
    } finally {
      runningRow = null;
    }
    onRunOne?.(name);
  }

  async function deleteRow(name: string) {
    const wf = $draftWorkflow;
    if (!wf) return;
    if (!confirm(`Delete test case "${name}"?`)) return;
    try {
      await workflowAPI.testDelete(wf.id, name);
      toastOk("Test case deleted");
      rows = rows.filter((r) => r.name !== name);
      await refresh();
    } catch (e: any) {
      toastError(e?.message ?? "delete failed");
    }
  }
</script>

<div class="flex items-center justify-between mb-2">
  <span class="text-xs">{rows.length} test case{rows.length === 1 ? "" : "s"}</span>
  <div class="flex items-center gap-2">
    <button
      class="px-2 py-1 rounded border border-slate-300 dark:border-[#2c3a5a] text-xs hover:bg-white-200 dark:hover:bg-[#1e293b]"
      onclick={openCreate}
    >
      + Add case
    </button>
    <button
      class="px-2 py-1 rounded bg-emerald-500 text-white-100 text-xs disabled:opacity-50"
      onclick={() => refresh()}
      disabled={running}
    >
      {running ? "Running…" : "Refresh"}
    </button>
  </div>
</div>

{#if rows.length === 0}
  <p class="text-xs text-black-700 dark:text-black-600">
    No test cases yet. Click <strong>+ Add case</strong> to create one, or capture
    a real run from the Runs panel.
  </p>
{:else}
  <ul class="divide-y divide-white-300 dark:divide-navy-600 dark:divide-[#2c3a5a]">
    {#each rows as c (c.name)}
      <li class="py-1.5 text-xs">
        <div class="flex items-center gap-3">
          <button
            class="flex-1 truncate font-mono text-left hover:text-emerald-600 dark:hover:text-emerald-400"
            onclick={() => openEdit(c.name)}
            title="Edit case"
          >
            {c.name}
          </button>
          <span class="text-black-700 dark:text-black-600">{c.assertions} assert</span>
          {#if c.duration_ms !== undefined}
            <span class="text-black-700 dark:text-black-600">{c.duration_ms}ms</span>
          {/if}
          {#if c.last_result === "pass"}
            <span class="px-1.5 py-0.5 rounded bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300">pass</span>
          {:else if c.last_result === "fail"}
            <span class="px-1.5 py-0.5 rounded bg-rose-100 text-rose-700 dark:bg-rose-900/40 dark:text-rose-300">fail</span>
          {/if}
          <button
            class="text-emerald-600 hover:underline disabled:opacity-50"
            onclick={() => runRow(c.name)}
            disabled={runningRow === c.name}
          >
            {runningRow === c.name ? "running…" : "run"}
          </button>
          <button
            class="text-rose-600 hover:underline"
            onclick={() => deleteRow(c.name)}
          >
            delete
          </button>
        </div>
        {#if c.last_result === "fail" && c.last_failures?.length}
          <ul class="mt-1 ml-3 list-disc text-rose-600 dark:text-rose-400">
            {#each c.last_failures as f}
              <li class="font-mono text-[11px]">{f}</li>
            {/each}
          </ul>
        {/if}
      </li>
    {/each}
  </ul>
{/if}

{#if modalCase}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
    <div class="w-[640px] max-h-[80vh] flex flex-col rounded-lg bg-white-100 dark:bg-[#0f172a] border border-slate-200 dark:border-[#2c3a5a] shadow-xl">
      <header class="px-4 py-2 border-b border-slate-200 dark:border-[#2c3a5a] flex items-center justify-between">
        <h3 class="text-sm font-semibold">
          {editingExisting ? "Edit test case" : "Add test case"}
        </h3>
        <button class="text-black-700 dark:text-black-600 hover:text-slate-900 dark:hover:text-black-800 dark:text-white-100" onclick={closeModal}>✕</button>
      </header>

      <div class="flex-1 overflow-y-auto p-4 space-y-3 text-xs">
        {#if modalLoading}
          <p class="text-black-700 dark:text-black-600">Loading…</p>
        {:else}
          <label class="block">
            <span class="block mb-1 text-black-600 dark:text-black-600">Name</span>
            <input
              type="text"
              class="w-full px-2 py-1 rounded border border-slate-300 dark:border-[#2c3a5a] bg-white-100 dark:bg-[#0f172a] font-mono disabled:opacity-50"
              bind:value={modalCase.name}
              placeholder="happy_path"
              disabled={editingExisting}
            />
            {#if editingExisting}
              <span class="text-[10px] text-black-700 dark:text-black-500">Name is the file slug and can't be renamed.</span>
            {/if}
          </label>

          <label class="block">
            <span class="block mb-1 text-black-600 dark:text-black-600">Trigger event (JSON)</span>
            <textarea
              class="w-full h-32 px-2 py-1 rounded border border-slate-300 dark:border-[#2c3a5a] bg-white-100 dark:bg-[#0f172a] font-mono text-[11px]"
              bind:value={inputJson}
              placeholder={'{\n  "Provider": "slack",\n  "ChannelID": "abc"\n}'}
            ></textarea>
            {#if inputErr}
              <span class="text-rose-600">{inputErr}</span>
            {:else}
              <span class="text-[10px] text-black-700 dark:text-black-500">Matches Go workflow.Event — fields like Provider, ChannelID, Body.</span>
            {/if}
          </label>

          <div>
            <div class="flex items-center justify-between mb-1">
              <span class="text-black-600 dark:text-black-600">Assertions</span>
              <button class="text-emerald-600 hover:underline" onclick={addAssertion}>+ add assertion</button>
            </div>
            {#if modalCase.assertions.length === 0}
              <p class="text-black-700 dark:text-black-500 italic">No assertions — the case will only check that the workflow runs without error.</p>
            {:else}
              <ul class="space-y-1.5">
                {#each modalCase.assertions as a, idx}
                  <li class="grid grid-cols-[1fr_auto_1fr_auto] gap-1.5 items-center">
                    <input
                      type="text"
                      class="px-2 py-1 rounded border border-slate-300 dark:border-[#2c3a5a] bg-white-100 dark:bg-[#0f172a] font-mono text-[11px]"
                      bind:value={a.subject}
                      placeholder="nodes.send.output.ok"
                    />
                    <select
                      class="px-2 py-1 rounded border border-slate-300 dark:border-[#2c3a5a] bg-white-100 dark:bg-[#0f172a]"
                      bind:value={a.operator}
                    >
                      {#each OPERATORS as op}
                        <option value={op}>{op}</option>
                      {/each}
                    </select>
                    {#if operatorTakesValue(a.operator)}
                      <input
                        type="text"
                        class="px-2 py-1 rounded border border-slate-300 dark:border-[#2c3a5a] bg-white-100 dark:bg-[#0f172a] font-mono text-[11px]"
                        bind:value={a.value}
                        placeholder="true / 42 / hello"
                      />
                    {:else}
                      <span class="text-black-700 dark:text-black-500 italic px-2">no value</span>
                    {/if}
                    <button
                      class="text-rose-600 hover:underline px-1"
                      onclick={() => removeAssertion(idx)}
                      title="Remove"
                    >
                      ✕
                    </button>
                  </li>
                {/each}
              </ul>
              <p class="mt-1 text-[10px] text-black-700 dark:text-black-500">
                Subjects use dotted paths: <code>nodes.&lt;id&gt;.output.&lt;field&gt;</code>,
                <code>trace.&lt;idx&gt;.status</code>. Values auto-coerce
                (<code>true</code>, <code>42</code>, <code>3.14</code>, <code>null</code>, JSON literals).
              </p>
            {/if}
          </div>

          {#if modalError}
            <p class="text-rose-600">{modalError}</p>
          {/if}
        {/if}
      </div>

      <footer class="px-4 py-2 border-t border-slate-200 dark:border-[#2c3a5a] flex items-center justify-end gap-2">
        <button
          class="px-3 py-1 rounded border border-slate-300 dark:border-[#2c3a5a] text-xs hover:bg-white-200 dark:hover:bg-[#1e293b]"
          onclick={closeModal}
          disabled={modalSaving}
        >
          Cancel
        </button>
        <button
          class="px-3 py-1 rounded bg-emerald-500 text-white-100 text-xs disabled:opacity-50"
          onclick={saveCase}
          disabled={modalSaving || modalLoading}
        >
          {modalSaving ? "Saving…" : editingExisting ? "Save" : "Create"}
        </button>
      </footer>
    </div>
  </div>
{/if}
