<script lang="ts">
  import { untrack } from "svelte";
  import type { AskRequest, AskAnswer, AskField, AskOption } from "../types/agents.js";

  type Props = {
    request: AskRequest | null;
    onSubmit: (answer: AskAnswer) => void;
    onDismiss?: () => void;
  };

  let { request, onSubmit, onDismiss }: Props = $props();

  /* ── helpers ──────────────────────────────────────────────────── */
  function parseArr(s: string | undefined): string[] | null {
    if (!s) return null;
    try {
      const a = JSON.parse(s);
      return Array.isArray(a) ? (a as string[]) : null;
    } catch {
      return null;
    }
  }

  /* ── single-question card state ───────────────────────────────── */
  let cardError = $state("");
  let freeformText = $state("");

  function handleOptionClick(opt: AskOption) {
    if (!request) return;
    onSubmit({ id: request.id, value: opt.value });
  }

  function handleFreeformSubmit(e: SubmitEvent) {
    e.preventDefault();
    if (!request) return;
    const text = freeformText.trim();
    if (!text) {
      cardError = "Type an answer, or click one of the options above.";
      return;
    }
    cardError = "";
    onSubmit({ id: request.id, text });
  }

  /* ── wizard state ─────────────────────────────────────────────── */
  let step = $state(0);
  let answers = $state<Record<string, string>>({});
  let stepError = $state("");

  let rankOrder = $state<string[]>([]);
  let choiceSelected = $state<string | null>(null);
  let multiSelected = $state<string[]>([]);
  let dropdownVal = $state("");
  let inputVal = $state("");
  let freeformVal = $state("");

  function fieldsArr(): AskField[] {
    return request?.fields ?? [];
  }

  function fieldAt(s: number): AskField | undefined {
    return fieldsArr()[s];
  }

  /* Load step state from answers; must be called with untrack to avoid
     triggering the $effect that watches request.id. */
  function loadStepState(s: number, ans: Record<string, string>) {
    const fields = fieldsArr();
    const f = fields[s];
    if (!f) return;
    const prev = ans[f.key];
    const opts = f.options ?? [];
    if (f.type === "rank") {
      rankOrder = parseArr(prev) ?? opts.map((o) => o.value);
    } else if (f.type === "choice") {
      choiceSelected = prev ?? null;
      freeformVal = "";
    } else if (f.type === "multi") {
      multiSelected = parseArr(prev) ?? [];
      freeformVal = "";
    } else if (f.type === "dropdown") {
      dropdownVal = prev ?? f.value ?? opts[0]?.value ?? "";
    } else {
      inputVal = prev ?? f.value ?? "";
    }
    stepError = "";
  }

  /* React to request changes (new ask appears) and reset wizard. Use
     untrack for all writes so Svelte doesn't see a read-write cycle. */
  $effect(() => {
    const req = request;
    if (req?.fields?.length) {
      untrack(() => {
        step = 0;
        answers = {};
        loadStepState(0, {});
      });
    }
  });

  /* Read the current value from step-local state. */
  function getStepValue(): string {
    const f = fieldAt(step);
    if (!f) return "";
    if (f.type === "rank") return JSON.stringify(rankOrder);
    if (f.type === "choice") {
      if (f.allow_freeform && freeformVal.trim() !== "") return freeformVal.trim();
      return choiceSelected ?? "";
    }
    if (f.type === "multi") {
      if (f.allow_freeform && freeformVal.trim() !== "") return freeformVal.trim();
      return multiSelected.length ? JSON.stringify(multiSelected) : "";
    }
    if (f.type === "dropdown") return dropdownVal.trim();
    return inputVal.trim();
  }

  function recordAndAdvance(skip: boolean) {
    const f = fieldAt(step);
    if (!f) return;
    const val = skip ? "" : getStepValue();
    if (!skip && f.required && !val) {
      stepError = `"${f.label ?? f.key}" is required — answer it to continue.`;
      return;
    }
    stepError = "";
    const newAnswers = { ...answers };
    if (val !== "") newAnswers[f.key] = val;
    else delete newAnswers[f.key];
    answers = newAnswers;

    const fields = fieldsArr();
    if (step >= fields.length - 1) {
      if (!request) return;
      onSubmit({ id: request.id, values: { ...newAnswers } });
      return;
    }
    const nextStep = step + 1;
    step = nextStep;
    loadStepState(nextStep, newAnswers);
  }

  function goBack() {
    if (step > 0) {
      const prevStep = step - 1;
      step = prevStep;
      loadStepState(prevStep, answers);
    }
  }

  /* Choice auto-advance after highlight. */
  function pickChoice(val: string) {
    choiceSelected = val;
    stepError = "";
    setTimeout(() => recordAndAdvance(false), 140);
  }

  function toggleMulti(val: string) {
    const idx = multiSelected.indexOf(val);
    if (idx >= 0) {
      multiSelected = multiSelected.filter((v) => v !== val);
    } else {
      multiSelected = [...multiSelected, val];
    }
    stepError = "";
  }

  /* Rank drag-drop. */
  function onDragStart(e: DragEvent, val: string) {
    e.dataTransfer?.setData("text/plain", val);
  }

  function onDrop(e: DragEvent, targetVal: string) {
    e.preventDefault();
    const fromVal = e.dataTransfer?.getData("text/plain");
    if (!fromVal || fromVal === targetVal) return;
    const fi = rankOrder.indexOf(fromVal);
    const ti = rankOrder.indexOf(targetVal);
    const next = [...rankOrder];
    next.splice(fi, 1);
    next.splice(ti, 0, fromVal);
    rankOrder = next;
  }

  /* Derived display helpers. */
  const isWizard = $derived(!!(request?.fields?.length));
  const totalSteps = $derived(fieldsArr().length);
  const field = $derived(fieldAt(step));
  const isLastStep = $derived(step >= totalSteps - 1);
  const progressLabel = $derived(totalSteps > 1 ? `${step + 1} / ${totalSteps}` : "");

  /* CSS constants (mirroring askuser.js). */
  const FIELD_INPUT_CLASS =
    "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none";
  const ROW_BASE =
    "w-full flex items-center gap-3 px-3 py-2.5 rounded-lg border text-left transition-colors cursor-pointer";
  const ROW_OFF =
    "border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-700";
  const ROW_ON = "border-green-500 bg-white-200 dark:bg-navy-700";
  const BADGE_BASE =
    "flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-xs font-semibold";
  const BADGE_OFF = "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600";
  const BADGE_ON = "bg-green-500 text-white-100";
</script>

{#if request !== null}
  {#if !isWizard}
    <!-- ── Single-question card ───────────────────────────────────── -->
    <div
      class="rounded-xl border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-900/20 px-4 py-3 space-y-3 shadow-sm"
    >
      <div class="flex items-start gap-3">
        <span
          class="mt-0.5 inline-flex h-2 w-2 shrink-0 rounded-full bg-amber-500 animate-pulse"
        ></span>
        <div class="flex-1 min-w-0">
          <div class="text-xs font-medium uppercase tracking-wide text-amber-700 dark:text-amber-300">
            Agent is asking you
          </div>
          <div class="mt-1 text-sm text-black-900 dark:text-white-100 whitespace-pre-wrap break-words">
            {request.question ?? ""}
          </div>
        </div>
      </div>

      {#if request.options?.length}
        <div class="flex flex-wrap gap-2">
          {#each request.options as opt (opt.value)}
            <button
              type="button"
              class="rounded-lg border border-amber-400 dark:border-amber-700 px-3 py-1.5 text-xs font-medium text-amber-700 dark:text-amber-300 hover:bg-amber-100 dark:hover:bg-amber-900/30 transition-colors"
              onclick={() => handleOptionClick(opt)}
            >
              {opt.label}
            </button>
          {/each}
        </div>
      {/if}

      {#if request.allow_freeform}
        <form class="flex flex-wrap gap-2 items-end" onsubmit={handleFreeformSubmit}>
          <input
            type="text"
            placeholder="Type your answer…"
            class="flex-1 min-w-0 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none"
            bind:value={freeformText}
            oninput={() => (cardError = "")}
          />
          <button
            type="submit"
            class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors"
          >
            Send
          </button>
        </form>
      {/if}

      {#if cardError}
        <p class="text-xs font-medium text-neg-400">{cardError}</p>
      {/if}
    </div>
  {:else}
    <!-- ── Wizard modal ─────────────────────────────────────────── -->
    <div
      class="fixed inset-0 z-50 flex items-end sm:items-center justify-center p-0 sm:p-4 bg-black-900/50"
    >
      <div
        class="flex w-full sm:max-w-lg max-h-[90vh] flex-col overflow-hidden rounded-t-xl sm:rounded-xl bg-white-100 dark:bg-navy-800 border border-white-300 dark:border-navy-600 shadow-xl"
      >
        <!-- Header -->
        <div class="flex items-start gap-3 px-5 pt-5 sm:px-6 sm:pt-6 shrink-0">
          <span
            class="mt-1 inline-flex h-2 w-2 shrink-0 rounded-full bg-amber-500 animate-pulse"
          ></span>
          <div class="flex-1 min-w-0">
            <div
              class="text-sm font-medium text-black-900 dark:text-white-100 whitespace-pre-wrap break-words"
            >
              {#if field}
                {(field.label ?? field.key)}{field.required ? " *" : ""}
              {/if}
            </div>
            {#if field?.help}
              <div class="mt-1 text-xs text-black-700 dark:text-black-600">{field.help}</div>
            {/if}
          </div>
          <div class="shrink-0 text-xs font-medium text-black-700 dark:text-black-600">
            {progressLabel}
          </div>
        </div>

        <!-- Body: field content -->
        <div class="flex-1 overflow-y-auto px-5 py-4 sm:px-6 space-y-2">
          {#if field?.type === "rank"}
            <div class="space-y-2">
              {#each rankOrder as val, i (val + "-" + i)}
                {@const opt = field.options?.find((o) => o.value === val) ?? { label: val, value: val }}
                <div
                  class="{ROW_BASE} {ROW_OFF} cursor-grab"
                  draggable="true"
                  ondragstart={(e) => onDragStart(e, val)}
                  ondragover={(e) => e.preventDefault()}
                  ondrop={(e) => onDrop(e, val)}
                  role="button"
                  tabindex="0"
                  onkeydown={() => {}}
                  aria-label="Drag to reorder: {opt.label}"
                >
                  <span class="{BADGE_BASE} {BADGE_OFF}">{i + 1}</span>
                  <div class="flex-1 min-w-0">
                    <div class="text-sm text-black-900 dark:text-white-100 truncate">
                      {opt.label}
                    </div>
                    {#if opt.description}
                      <div class="text-xs text-black-700 dark:text-black-600 truncate">
                        {opt.description}
                      </div>
                    {/if}
                  </div>
                  <span
                    class="shrink-0 text-black-700 dark:text-black-600 text-lg leading-none select-none"
                  >≡</span>
                </div>
              {/each}
            </div>

          {:else if field?.type === "choice" || field?.type === "multi"}
            {@const isMulti = field.type === "multi"}
            <div class="space-y-2">
              {#each field.options ?? [] as opt, i (opt.value)}
                {@const on = isMulti
                  ? multiSelected.includes(opt.value)
                  : choiceSelected === opt.value}
                <button
                  type="button"
                  class="{ROW_BASE} {on ? ROW_ON : ROW_OFF}"
                  onclick={() => {
                    if (isMulti) {
                      toggleMulti(opt.value);
                    } else {
                      pickChoice(opt.value);
                    }
                  }}
                >
                  <span class="{BADGE_BASE} {on ? BADGE_ON : BADGE_OFF}">
                    {isMulti ? (on ? "✓" : "") : String(i + 1)}
                  </span>
                  <div class="flex-1 min-w-0">
                    <div class="text-sm text-black-900 dark:text-white-100 truncate">
                      {opt.label}
                    </div>
                    {#if opt.description}
                      <div class="text-xs text-black-700 dark:text-black-600 truncate">
                        {opt.description}
                      </div>
                    {/if}
                  </div>
                </button>
              {/each}
            </div>
            {#if field.allow_freeform}
              <input
                type="text"
                placeholder={field.placeholder ?? "Other…"}
                class={FIELD_INPUT_CLASS}
                bind:value={freeformVal}
                oninput={() => (stepError = "")}
              />
            {/if}
            {#if isMulti}
              <div class="text-xs text-black-700 dark:text-black-600">
                {multiSelected.length} selected
              </div>
            {/if}

          {:else if field?.type === "dropdown"}
            <select
              class={FIELD_INPUT_CLASS}
              bind:value={dropdownVal}
              onchange={() => (stepError = "")}
            >
              {#each field.options ?? [] as opt (opt.value)}
                <option value={opt.value}>{opt.label ?? opt.value}</option>
              {/each}
            </select>

          {:else if field}
            <input
              type={field.type === "secret"
                ? "password"
                : field.type === "number"
                  ? "number"
                  : "text"}
              autocomplete={field.type === "secret" ? "new-password" : undefined}
              placeholder={field.placeholder ?? ""}
              class={FIELD_INPUT_CLASS}
              bind:value={inputVal}
              oninput={() => (stepError = "")}
              onkeydown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  recordAndAdvance(false);
                }
              }}
            />
          {/if}
        </div>

        <!-- Error line -->
        {#if stepError}
          <p class="px-5 sm:px-6 text-xs font-medium text-neg-400">{stepError}</p>
        {/if}

        <!-- Footer -->
        <div
          class="flex items-center justify-between gap-2 px-5 pb-5 pt-3 sm:px-6 sm:pb-6 shrink-0 border-t border-white-200 dark:border-navy-700"
        >
          {#if !(field?.required)}
            <button
              type="button"
              class="rounded-lg px-3 py-2 text-sm font-medium text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
              onclick={() => recordAndAdvance(true)}
            >
              Skip
            </button>
          {:else}
            <span></span>
          {/if}

          <div class="flex items-center gap-2">
            {#if step > 0}
              <button
                type="button"
                class="rounded-lg border border-white-400 dark:border-navy-600 px-4 py-2 text-sm font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
                onclick={goBack}
              >
                Back
              </button>
            {/if}
            <button
              type="button"
              class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors"
              onclick={() => recordAndAdvance(false)}
            >
              {isLastStep ? "Submit" : "Next"}
            </button>
          </div>
        </div>
      </div>
    </div>
  {/if}
{/if}
