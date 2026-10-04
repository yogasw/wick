<script lang="ts">
  /* The four Slack setting groups of a connected agent — Access Control
     (people, groups, bots, channels), Agent Behaviour, Reaction Auto-Reply,
     Approval Gates — rendered from the same config tags as the Channels
     page. Every group starts closed; a click opens it. Each change saves
     its key right away. onSummary feeds the card header. */
  import { untrack } from "svelte";
  import { runApi, type AgentItem } from "../../api/team.js";
  import {
    getAgentSlackSettings, setAgentSlackSetting, lookupAgentSlack, settingGroups, pickerItems, isVisible,
    peopleChoice, peopleValues, accessSummary, isOpenToWorkspace, OPTION_LABEL,
    type SlackSettingField, type PickerItem, type PeopleChoice,
  } from "../../slackAccess.js";

  type Props = { base: string; agent: AgentItem; onSummary?: (summary: string, open: boolean) => void };
  let { base, agent, onSummary }: Props = $props();

  const muted = "text-xs text-black-800 dark:text-black-600";
  const input = "w-full rounded-lg border border-white-300 bg-white-100 px-2 py-1 text-xs text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  /* Users/groups fields only show under "Specific people & groups". */
  const PEOPLE_KEYS = new Set(["users_mode", "allowed_users", "groups_mode", "allowed_groups"]);

  let fields = $state<SlackSettingField[]>([]);
  let values = $state<Record<string, string>>({});
  let ownerID = $state("");
  let ownerName = $state("");
  let ownerHandle = $state("");
  let loadError = $state("");
  let saveState = $state<"" | "saving" | "saved" | "error">("");
  let customPeople = $state(false);
  let queries = $state<Record<string, string>>({});
  let results = $state<Record<string, PickerItem[]>>({});

  function apply(s: { fields: SlackSettingField[]; owner_slack_id?: string; owner_slack_name?: string }) {
    fields = s.fields;
    values = Object.fromEntries(s.fields.map((f) => [f.key, f.value]));
    ownerID = s.owner_slack_id ?? "";
    ownerName = s.owner_slack_name ?? "";
    ownerHandle = s.owner_slack_handle ?? "";
  }

  $effect(() => {
    const id = agent.id;
    untrack(() => {
      runApi(getAgentSlackSettings(base, id)).then(apply).catch((e) => (loadError = String(e?.message ?? e)));
    });
  });

  const groups = $derived(settingGroups(fields));
  const choice = $derived<PeopleChoice>(customPeople ? "custom" : peopleChoice(values, ownerID));
  $effect(() => {
    if (fields.length) onSummary?.(accessSummary(values, ownerID, ownerName), isOpenToWorkspace(values));
  });

  async function save(pairs: [string, string][]) {
    if (!pairs.length) return;
    saveState = "saving";
    const prev = { ...values };
    for (const [k, v] of pairs) values[k] = v;
    try {
      let s;
      for (const [k, v] of pairs) s = await runApi(setAgentSlackSetting(base, agent.id, k, v));
      if (s) apply(s);
      saveState = "saved";
    } catch {
      values = prev;
      saveState = "error";
    }
  }

  function pickPeople(c: PeopleChoice) {
    customPeople = c === "custom";
    void save(peopleValues(c, values, ownerID ? { id: ownerID, name: ownerName || ownerID } : undefined));
  }

  async function search(f: SlackSettingField, q: string) {
    queries[f.key] = q;
    if (!q.trim()) { results[f.key] = []; return; }
    try {
      const r = await runApi(lookupAgentSlack(base, agent.id, f.options ?? "", q));
      if (queries[f.key] === q) results[f.key] = r.items;
    } catch {
      results[f.key] = [];
    }
  }

  function addItem(f: SlackSettingField, it: PickerItem) {
    const cur = pickerItems(values[f.key]);
    if (!cur.some((x) => x.id === it.id)) void save([[f.key, JSON.stringify([...cur, it])]]);
    queries[f.key] = "";
    results[f.key] = [];
  }
  const removeItem = (f: SlackSettingField, id: string) =>
    save([[f.key, JSON.stringify(pickerItems(values[f.key]).filter((x) => x.id !== id))]]);

  const shown = (f: SlackSettingField) =>
    isVisible(f, values) && !(f.group === "Access Control" && PEOPLE_KEYS.has(f.key) && choice !== "custom");
  const label = (k: string) => k.replace(/_/g, " ").replace(/^./, (c) => c.toUpperCase());
</script>

<div class="mt-4 space-y-3" data-testid="slack-settings">
  {#if loadError}<p class="text-xs text-neg-400">{loadError}</p>{/if}
  {#if saveState}<p class={muted} aria-live="polite">{saveState === "saving" ? "Saving…" : saveState === "saved" ? "Saved" : "Couldn't save"}</p>{/if}
  {#each groups as g (g.title)}
    <details class="group/card rounded-xl border border-white-300 dark:border-navy-600" data-testid="slack-group" data-group={g.title}>
      <summary class="flex cursor-pointer list-none items-start gap-2 px-4 py-3 select-none">
        <svg class="mt-0.5 h-4 w-4 shrink-0 text-black-700 transition-transform group-open/card:rotate-90 dark:text-black-600" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true"><path fill-rule="evenodd" d="M7.21 14.77a.75.75 0 0 1 .02-1.06L11.168 10 7.23 6.29a.75.75 0 1 1 1.04-1.08l4.5 4.25a.75.75 0 0 1 0 1.08l-4.5 4.25a.75.75 0 0 1-1.06-.02Z" clip-rule="evenodd" /></svg>
        <span>
          <span class="block text-sm font-medium text-black-900 dark:text-white-100">{g.title === "Access Control" ? `Who can use @${agent.handle}` : g.title}</span>
          {#if g.title === "Access Control"}<span class="block {muted}">Access Control</span>{/if}
          {#if g.desc}<span class="block whitespace-pre-line {muted}">{g.desc}</span>{/if}
        </span>
      </summary>
      <div class="space-y-3 border-t border-white-300 px-4 py-3 dark:border-navy-600">
        {#if g.title === "Access Control"}
          <fieldset data-testid="people-choice">
            <legend class="text-xs font-medium text-black-900 dark:text-white-100">People</legend>
            {#each [["all", "Everyone in the workspace"], ["me", ownerID ? `Only me (@${ownerHandle || ownerName} · ${ownerID})` : "Only me"], ["custom", "Specific people & groups"]] as [c, lbl] (c)}
              <label class="mt-1 flex items-center gap-2 text-xs text-black-900 dark:text-white-100">
                <input type="radio" name="people-{agent.id}" value={c} checked={choice === c} disabled={c === "me" && !ownerID} onchange={() => pickPeople(c as PeopleChoice)} />
                {lbl}
              </label>
            {/each}
            {#if !ownerID}<p class="mt-1 text-xs text-neg-400" data-testid="owner-unresolved">Your account wasn't found in this workspace — pick people manually.</p>{/if}
          </fieldset>
        {/if}
        {#each g.fields as f (f.key)}
          {#if shown(f)}
            <div data-field={f.key}>
              <label class="block text-xs font-medium text-black-900 dark:text-white-100" for="sf-{agent.id}-{f.key}">{label(f.key)}</label>
              {#if f.type === "dropdown"}
                <select id="sf-{agent.id}-{f.key}" class="mt-1 {input}" value={values[f.key]} onchange={(e) => save([[f.key, e.currentTarget.value]])}>
                  {#each (f.options ?? "").split("|").filter(Boolean) as o (o)}
                    <option value={o}>{OPTION_LABEL[f.key]?.[o] ?? o}</option>
                  {/each}
                </select>
              {:else if f.type === "bool" || f.type === "checkbox" || f.type === "boolean"}
                <input id="sf-{agent.id}-{f.key}" type="checkbox" class="mt-1" checked={values[f.key] === "true"} onchange={(e) => save([[f.key, String(e.currentTarget.checked)]])} />
              {:else if f.type === "picker"}
                <div class="mt-1 flex flex-wrap gap-1">
                  {#each pickerItems(values[f.key]) as it (it.id)}
                    <span class="inline-flex items-center gap-1 rounded-full bg-white-200 px-2 py-0.5 text-xs text-black-900 dark:bg-navy-600 dark:text-white-100">
                      {it.name}<button type="button" aria-label="Remove {it.name}" onclick={() => removeItem(f, it.id)}>×</button>
                    </span>
                  {/each}
                </div>
                <input id="sf-{agent.id}-{f.key}" class="mt-1 {input}" placeholder="Search…" value={queries[f.key] ?? ""} oninput={(e) => search(f, e.currentTarget.value)} />
                {#if results[f.key]?.length}
                  <ul class="mt-1 rounded-lg border border-white-300 dark:border-navy-600">
                    {#each results[f.key] as it (it.id)}
                      <li><button type="button" class="w-full px-2 py-1 text-left text-xs text-black-900 dark:text-white-100" onclick={() => addItem(f, it)}>{it.name} <span class={muted}>{it.id}</span></button></li>
                    {/each}
                  </ul>
                {/if}
              {:else}
                <input id="sf-{agent.id}-{f.key}" class="mt-1 {input}" value={values[f.key]} onchange={(e) => save([[f.key, e.currentTarget.value]])} />
              {/if}
              {#if f.desc}<p class="mt-1 {muted}">{f.desc}</p>{/if}
            </div>
          {/if}
        {/each}
      </div>
    </details>
  {/each}
</div>
