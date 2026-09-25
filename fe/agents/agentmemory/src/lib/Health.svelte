<script lang="ts">
  /* Health tab — the failures nobody sees without a UI (PLAN §13.3).

     Three checks, each with its repair next to it: capture coverage (a
     harness running here while nothing is captured), the cross-project
     contamination audit (client data filed under the wrong project), and the
     zero-LLM state — which is reported by its real consequence, not its name.

     These are the expensive reads in this feature: doctor walks the local
     harness stores and the audit scans the whole store. They run when this
     tab is opened or refreshed by hand, never on the panel's poll
     (PLAN §13.5 point 7). */
  import { Button } from "@wick-fe/common-ui";
  import Section from "./Section.svelte";
  import BlockedState from "./BlockedState.svelte";
  import { contaminationRows, healthFindings, healthVerdict, levelClasses } from "./health.js";
  import { blockedBy } from "./format.js";
  import type { HealthReport, Overview, Scope } from "./types.js";

  type Props = {
    report: HealthReport | null;
    ov: Overview | null;
    scope: Scope;
    loading: boolean;
    onRefresh: () => void;
    onGoOverview: () => void;
  };
  let { report, ov, scope, loading, onRefresh, onGoOverview }: Props = $props();

  const blocked = $derived(blockedBy(report));
  const findings = $derived(healthFindings(report?.doctor, report?.contamination, ov?.store, report?.collisions));
  const rows = $derived(contaminationRows(report?.contamination));
  const columns = $derived(rows.length ? Object.keys(rows[0]) : []);

  // Capture coverage is resolved from a working directory, so it is about ONE
  // project — never the whole store. Saying which one is the difference
  // between a finding and a rumour (PLAN §13.5 point 1, §14).
  const doctorScope = $derived(
    report?.doctor?.project
      ? `${report.doctor.workspace ?? "default"}/${report.doctor.project}`
      : scope.project
        ? `${scope.workspace ?? "default"}/${scope.project}`
        : "the project wick itself runs in",
  );
</script>

<div class="mx-auto w-full max-w-4xl space-y-5 px-4 py-5 sm:px-6 sm:py-6">
  <div class="flex flex-wrap items-center justify-between gap-3">
    <div class="min-w-0">
      <p class="text-sm font-medium text-black-900 dark:text-white-100">
        {loading ? "Running the checks…" : healthVerdict(findings)}
      </p>
      <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
        Capture coverage is for <span class="font-mono">{doctorScope}</span>; the contamination audit covers the whole
        store.
      </p>
    </div>
    <Button variant="secondary" size="md" disabled={loading} onclick={onRefresh}>Re-run checks</Button>
  </div>

  {#if blocked}
    <BlockedState {blocked} onAction={onGoOverview} />
  {:else if loading && !report}
    <div
      class="rounded-xl border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-10 text-center text-xs text-black-700 dark:text-black-600"
    >
      Reading the local harness stores and auditing the database — this one is slower than the other tabs on purpose.
    </div>
  {:else if !report}
    <div
      class="rounded-xl border border-dashed border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-10 text-center"
    >
      <p class="text-sm font-medium text-black-900 dark:text-white-100">Nothing has been checked yet</p>
      <p class="mx-auto mt-1.5 max-w-md text-xs leading-relaxed text-black-700 dark:text-black-600">
        These checks read every local harness session store and scan the whole database, so they do not run on their
        own. Press “Re-run checks”.
      </p>
    </div>
  {:else}
    {#each findings as f (f.id)}
      {@const cls = levelClasses(f.level)}
      <div class={`rounded-xl border bg-white-100 dark:bg-navy-700 ${cls.border}`}>
        <div class="flex flex-wrap items-start justify-between gap-2 px-5 py-3">
          <h2 class={`text-sm font-medium ${cls.title}`}>{f.title}</h2>
          <span class={`rounded-full px-2 py-0.5 text-[0.6875rem] font-medium uppercase tracking-wider ${cls.chip}`}>
            {f.level}
          </span>
        </div>
        <p class="px-5 pb-3 text-xs leading-relaxed text-black-800 dark:text-black-600">{f.body}</p>
        {#if f.fix}
          <div class="border-t border-white-300 dark:border-navy-600 px-5 py-3">
            <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">Fix</p>
            <p class="mt-1 text-xs leading-relaxed text-black-900 dark:text-white-100">{f.fix.label}</p>
            {#if f.fix.command}
              <code
                class="mt-1.5 block overflow-x-auto rounded-lg bg-white-200 dark:bg-navy-800 px-3 py-2 font-mono text-xs text-black-900 dark:text-white-100"
                >{f.fix.command}</code
              >
            {:else if f.fix.where}
              <p class="mt-1 text-xs text-black-700 dark:text-black-600">
                In wick: <span class="font-medium text-black-900 dark:text-white-100">{f.fix.where}</span>
              </p>
            {/if}
          </div>
        {/if}
      </div>
    {/each}

    {#if rows.length}
      <!-- The audit's own rows. Their field names are whatever the backend
           sends: the only run available to read off was an empty one, so the
           table renders the keys that arrive rather than dropping the rows
           that matter most. -->
      <Section
        title="Contaminated sessions"
        scope="whole store"
        note="Straight from the backend's audit. A page under the wrong project is readable by anyone working in that project."
      >
        <div class="overflow-x-auto">
          <table class="w-full text-left text-xs">
            <thead class="border-b border-white-300 dark:border-navy-600">
              <tr>
                {#each columns as c (c)}
                  <th class="px-5 py-2 font-medium uppercase tracking-wider text-black-700 dark:text-black-600">{c}</th>
                {/each}
              </tr>
            </thead>
            <tbody class="divide-y divide-white-300 dark:divide-navy-600">
              {#each rows as r, i (i)}
                <tr>
                  {#each columns as c (c)}
                    <td class="px-5 py-2 font-mono text-black-900 dark:text-white-100">{r[c] ?? "—"}</td>
                  {/each}
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </Section>
    {/if}
  {/if}
</div>
