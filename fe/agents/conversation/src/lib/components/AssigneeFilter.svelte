<script lang="ts">
  /* The board's assignee filter: a button that sums up who is ticked, over
     a list of checkboxes — Everyone, Unassigned, Me, then the people. Any
     number can be ticked; a ticket shows when ANY of its assignees is one
     of them, and nothing ticked is everyone. The parent owns the list (it
     is part of the saved filter and of the board's request); this only
     edits it.

     Keyboard: ArrowUp/ArrowDown walk the search box and the boxes, Space
     ticks, Escape closes and hands focus back to the button. A click
     anywhere outside closes it too. */
  import { ASSIGNEE_ME, ASSIGNEE_NONE, assigneeSummary } from "../ticketAssigneeFilter.js";

  type Props = {
    /* The ticked values: user ids, "me", "none". */
    selected: string[];
    /* Everyone the board names, the viewer excluded (they are "Me"). */
    people: { id: string; name: string }[];
    /* The viewer's id. Absent (nobody logged in) hides the Me row. */
    me?: string;
    onChange: (next: string[]) => void;
  };

  let { selected, people, me, onChange }: Props = $props();

  let open = $state(false);
  let query = $state("");
  let root = $state<HTMLDivElement>();
  let button = $state<HTMLButtonElement>();
  let panel = $state<HTMLDivElement>();
  const panelId = `assignee-filter-${Math.random().toString(36).slice(2, 8)}`;

  const names = $derived(Object.fromEntries(people.map((p) => [p.id, p.name])));
  /* A ticked id the board no longer names (their tickets are gone) still
     gets a row, so it can be unticked from here. */
  const rows = $derived.by(() => {
    const known = new Set(people.map((p) => p.id));
    const stray = selected
      .filter((v) => v !== ASSIGNEE_ME && v !== ASSIGNEE_NONE && v !== me && !known.has(v))
      .map((id) => ({ id, name: id }));
    return [...people, ...stray];
  });
  const q = $derived(query.trim().toLowerCase());
  const shown = $derived(q === "" ? rows : rows.filter((p) => p.name.toLowerCase().includes(q)));
  const summary = $derived(assigneeSummary(selected, names, me));
  const isOn = (v: string) => selected.includes(v);

  function toggle(v: string) {
    onChange(isOn(v) ? selected.filter((x) => x !== v) : [...selected, v]);
  }
  /* Everyone clears the rest; unticking it has nothing to undo. */
  function everyone() {
    if (selected.length > 0) onChange([]);
  }

  function close(refocus: boolean) {
    open = false;
    query = "";
    if (refocus) button?.focus();
  }

  function onWindowPointerDown(e: PointerEvent) {
    if (open && root && !root.contains(e.target as Node)) close(false);
  }

  function onPanelKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close(true);
      return;
    }
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    const stops = Array.from(panel?.querySelectorAll<HTMLElement>("input") ?? []);
    if (stops.length === 0) return;
    e.preventDefault();
    const at = stops.indexOf(document.activeElement as HTMLElement);
    const next = e.key === "ArrowDown" ? (at + 1) % stops.length : (at <= 0 ? stops.length : at) - 1;
    stops[next].focus();
  }

  function onButtonKeydown(e: KeyboardEvent) {
    if (e.key === "ArrowDown" && !open) {
      e.preventDefault();
      open = true;
    }
  }

  // Opening puts the cursor in the search box, so typing narrows at once.
  $effect(() => {
    if (open) panel?.querySelector<HTMLInputElement>("input")?.focus();
  });

  const rowCls = "flex cursor-pointer items-center gap-2 rounded px-2 py-1 hover:bg-white-200 dark:hover:bg-navy-700";
  const boxCls = "h-3.5 w-3.5 shrink-0 accent-green-500";
</script>

<svelte:window onpointerdown={onWindowPointerDown} />

<div class="relative" bind:this={root} data-testid="assignee-filter">
  <button
    bind:this={button}
    type="button"
    aria-haspopup="true"
    aria-expanded={open}
    aria-controls={panelId}
    data-testid="assignee-filter-button"
    onclick={() => (open ? close(false) : (open = true))}
    onkeydown={onButtonKeydown}
    class={[
      "inline-flex items-center gap-1.5 rounded-lg border bg-white-100 px-3 py-1.5 text-xs text-black-800 focus:border-green-500 focus:outline-none dark:bg-navy-700 dark:text-white-100",
      selected.length > 0 ? "border-green-500" : "border-white-400 dark:border-navy-600",
    ].join(" ")}
  >
    <span class="max-w-[14rem] truncate">Assignee: {summary}</span>
    <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-black-600" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
      <path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path>
    </svg>
  </button>

  {#if open}
    <!-- The keys come from the inputs inside; the panel only catches them
         on their way up, so one handler walks every row. -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
      bind:this={panel}
      id={panelId}
      role="group"
      aria-label="Filter by assignee"
      tabindex="-1"
      onkeydown={onPanelKeydown}
      class="absolute right-0 z-30 mt-1 w-64 rounded-lg border border-white-400 bg-white-100 p-2 text-xs text-black-800 shadow-lg dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
    >
      <input
        bind:value={query}
        type="search"
        placeholder="Search people…"
        aria-label="Search people"
        data-testid="assignee-filter-search"
        class="mb-1.5 w-full rounded-md border border-white-400 bg-white-100 px-2 py-1 text-xs text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
      />
      <div class="max-h-64 overflow-y-auto">
        <label class={rowCls}>
          <input type="checkbox" class={boxCls} checked={selected.length === 0} onchange={everyone} />
          <span class="font-semibold">Everyone</span>
          <span class="ml-auto text-[10px] text-black-600">clears the rest</span>
        </label>
        <label class={rowCls}>
          <input type="checkbox" class={boxCls} checked={isOn(ASSIGNEE_NONE)} onchange={() => toggle(ASSIGNEE_NONE)} />
          <span>Unassigned</span>
        </label>
        <div class="my-1 border-t border-white-300 dark:border-navy-600"></div>
        {#if me}
          <label class={rowCls}>
            <input type="checkbox" class={boxCls} checked={isOn(ASSIGNEE_ME) || isOn(me)} onchange={() => toggle(isOn(me) ? me : ASSIGNEE_ME)} />
            <span>Me</span>
          </label>
        {/if}
        {#each shown as p (p.id)}
          <label class={rowCls}>
            <input type="checkbox" class={boxCls} checked={isOn(p.id)} onchange={() => toggle(p.id)} />
            <span class="min-w-0 truncate">{p.name}</span>
          </label>
        {/each}
        {#if q !== "" && shown.length === 0}
          <p class="px-2 py-1 text-black-600">No one matches “{query.trim()}”.</p>
        {/if}
      </div>
      <div class="mt-1.5 flex items-center justify-between border-t border-white-300 px-1 pt-1.5 text-[10px] text-black-600 dark:border-navy-600">
        <button
          type="button"
          disabled={selected.length === 0}
          onclick={() => onChange([])}
          class="font-medium hover:text-black-900 disabled:opacity-40 dark:hover:text-white-100"
        >Clear</button>
        <span>Saved per project</span>
      </div>
    </div>
  {/if}
</div>
