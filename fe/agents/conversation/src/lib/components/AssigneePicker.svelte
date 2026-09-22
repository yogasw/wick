<script lang="ts">
  /* Who a ticket is on — by NAME, and as many people as the work needs.

     It used to be a text box holding one wick user id, which asked whoever
     was assigning to know a uuid and could only ever hold one person. Work
     lands on a pair often enough that the second name was being written
     into the title instead.

     So: the people already on it are chips, each removable, and adding
     someone is a select of names. The list of candidates is fetched once
     per project — it changes when somebody joins wick, not when a ticket
     moves — and a lookup failure leaves the chips alone rather than losing
     the assignees that are already there. */
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { listAssignees, type AssigneeOption } from "../api/tickets.js";

  type Props = {
    base: string;
    projectId?: string;
    /* Who is on the ticket now, in order. The first is the one a board card
       has room to show. */
    assignees: string[];
    /* Names already known (the ticket payload resolves its own people), so
       the chips read correctly before the candidate list lands. */
    users?: Record<string, string>;
    /* The caller, for "take it". */
    me?: string;
    /* The new full list. Replaces — the API contract is the same. */
    onChange: (next: string[]) => void;
  };

  let { base, projectId, assignees, users, me, onChange }: Props = $props();

  let options = $state<AssigneeOption[]>([]);
  let loadFailed = $state(false);

  $effect(() => {
    const pid = projectId;
    if (!pid) return;
    Effect.runPromise(listAssignees(base, pid).pipe(Effect.provide(WickClientLayer)))
      .then((list) => {
        options = list;
        loadFailed = false;
      })
      .catch(() => {
        // An older server has no such endpoint. The chips still work; only
        // the "add someone" select has nothing to offer.
        loadFailed = true;
      });
  });

  /* A name from wherever one is known: the fetched list first, then the
     names that came with the ticket, then the id — which at least says
     SOMEBODY is on it. */
  function nameOf(id: string): string {
    return options.find((o) => o.id === id)?.name ?? users?.[id] ?? id;
  }

  const current = $derived(assignees.filter((a) => a.trim() !== ""));
  /* Only people not already on it: an "add" list offering someone who is
     right there in a chip is a click that does nothing. */
  const addable = $derived(options.filter((o) => !current.includes(o.id)));
  const canTakeIt = $derived(!!me && !current.includes(me));

  function add(id: string) {
    if (!id || current.includes(id)) return;
    onChange([...current, id]);
  }

  function remove(id: string) {
    onChange(current.filter((a) => a !== id));
  }

  function initial(name: string): string {
    return (name.trim()[0] ?? "?").toUpperCase();
  }
</script>

<div data-testid="assignee-picker">
  <div class="mb-1 flex items-center justify-between">
    <span class="block text-xs font-medium text-black-800 dark:text-black-600">Assignees</span>
    <!-- "take it" is a link beside the label, the Zendesk idiom — claiming a
         ticket is one click, not a form control. It ADDS now rather than
         replacing: taking a shared ticket on must not push the colleague
         already on it off. -->
    {#if canTakeIt}
      <button
        type="button"
        data-testid="assignee-take-it"
        onclick={() => me && add(me)}
        class="text-[11px] font-medium text-green-600 transition-colors hover:underline dark:text-green-400"
      >take it</button>
    {/if}
  </div>

  {#if current.length > 0}
    <ul class="mb-1.5 flex flex-wrap gap-1">
      {#each current as id (id)}
        <li
          class="inline-flex max-w-full items-center gap-1 rounded-full bg-white-200 py-0.5 pl-0.5 pr-1 dark:bg-navy-800"
        >
          <span
            class="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-green-500 text-[9px] font-semibold text-white-100"
            aria-hidden="true"
          >{initial(nameOf(id))}</span>
          <span class="min-w-0 truncate text-[11px] text-black-900 dark:text-white-100" title={nameOf(id)}
          >{nameOf(id)}</span>
          <button
            type="button"
            aria-label={`Remove ${nameOf(id)}`}
            onclick={() => remove(id)}
            class="shrink-0 rounded-full px-0.5 text-[11px] leading-none text-black-700 transition-colors hover:text-neg-400 dark:text-black-600"
          >×</button>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="mb-1.5 text-[11px] text-black-700 dark:text-black-600">Unassigned</p>
  {/if}

  {#if addable.length > 0}
    <!-- Resets to the placeholder after every pick, so the control reads as
         "add someone" rather than as the current value of anything. -->
    <select
      aria-label="Add an assignee"
      data-testid="assignee-add"
      value=""
      onchange={(e) => {
        const el = e.target as HTMLSelectElement;
        add(el.value);
        el.value = "";
      }}
      class="w-full rounded-lg border border-white-400 bg-white-100 px-2.5 py-1.5 text-xs text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
    >
      <option value="">Add someone…</option>
      {#each addable as o (o.id)}
        <option value={o.id}>{o.name}</option>
      {/each}
    </select>
  {:else if loadFailed}
    <p class="text-[11px] text-black-700 dark:text-black-600">
      Could not load the list of people.
    </p>
  {:else if options.length > 0}
    <p class="text-[11px] text-black-700 dark:text-black-600">Everyone is already on this.</p>
  {/if}
</div>
