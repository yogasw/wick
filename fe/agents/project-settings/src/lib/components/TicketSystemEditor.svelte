<script lang="ts">
  import type { AutoCreateRule, TicketConfig, TicketField } from "$lib/types.js";

  type Props = {
    cfg: TicketConfig;
    onChange: (cfg: TicketConfig) => void;
  };

  let { cfg, onChange }: Props = $props();

  const enabled = $derived(cfg.enabled === true);
  const fields = $derived(cfg.fields ?? []);
  const followupMin = $derived(Math.round((cfg.followup_after_sec ?? 0) / 60));
  const resolveDays = $derived(
    Math.round(((cfg.auto_resolve_after_sec ?? 0) / 86400) * 10) / 10,
  );

  function patch(p: Partial<TicketConfig>) {
    onChange({ ...cfg, ...p });
  }

  function patchField(i: number, p: Partial<TicketField>) {
    const next = fields.map((f, idx) => (idx === i ? { ...f, ...p } : f));
    patch({ fields: next });
  }

  function addField() {
    patch({ fields: [...fields, { key: "", label: "", type: "text" }] });
  }

  function removeField(i: number) {
    patch({ fields: fields.filter((_, idx) => idx !== i) });
  }

  /* Options edit as comma-separated text; parsed on the way out. */
  function setOptions(i: number, raw: string) {
    patchField(i, {
      options: raw.split(",").map((s) => s.trim()).filter((s) => s !== ""),
    });
  }

  /* ── auto-create rules ── */
  const rules = $derived(cfg.auto_create ?? []);

  const ORIGINS = [
    { value: "*", label: "Any origin" },
    { value: "ui", label: "Dashboard" },
    { value: "slack", label: "Slack" },
    { value: "telegram", label: "Telegram" },
    { value: "rest", label: "API" },
  ];

  function patchRule(i: number, p: Partial<AutoCreateRule>) {
    patch({ auto_create: rules.map((r, idx) => (idx === i ? { ...r, ...p } : r)) });
  }

  function addRule() {
    patch({ auto_create: [...rules, { origin: "*", enabled: true }] });
  }

  function removeRule(i: number) {
    patch({ auto_create: rules.filter((_, idx) => idx !== i) });
  }

  /* Order is meaning here, not preference: the first matching rule decides,
     so moving a rule up is how an exception gets to out-rank the broad rule
     it carves into. */
  function moveRule(i: number, delta: number) {
    const j = i + delta;
    if (j < 0 || j >= rules.length) return;
    const next = [...rules];
    [next[i], next[j]] = [next[j], next[i]];
    patch({ auto_create: next });
  }

  /* The Match field is one string with a prefix ("contains:" / "regex:").
     The UI splits it into a kind and a value so nobody has to remember the
     prefix, then joins it back. */
  function matchKind(r: AutoCreateRule): "any" | "contains" | "regex" {
    const m = (r.match ?? "").trim();
    if (m.startsWith("contains:")) return "contains";
    if (m.startsWith("regex:")) return "regex";
    return "any";
  }

  function matchValue(r: AutoCreateRule): string {
    const m = (r.match ?? "").trim();
    if (m.startsWith("contains:")) return m.slice("contains:".length);
    if (m.startsWith("regex:")) return m.slice("regex:".length);
    return "";
  }

  function setMatchKind(i: number, kind: string) {
    const v = matchValue(rules[i]);
    patchRule(i, { match: kind === "any" ? "" : `${kind}:${v}` });
  }

  function setMatchValue(i: number, value: string) {
    const kind = matchKind(rules[i]);
    patchRule(i, { match: kind === "any" ? "" : `${kind}:${value}` });
  }

  /* A regex that will not compile is refused by the server on save; showing
     it here means the operator learns before clicking rather than after. */
  function regexError(r: AutoCreateRule): string {
    if (matchKind(r) !== "regex") return "";
    const v = matchValue(r);
    if (v.trim() === "") return "needs an expression";
    try {
      new RegExp(v);
      return "";
    } catch (e) {
      return e instanceof Error ? e.message : "invalid";
    }
  }

  /* One-line plain-English read of a rule, so a list of them can be checked
     at a glance instead of decoded field by field. */
  function ruleSummary(r: AutoCreateRule): string {
    const origin = r.origin === "*" ? "any origin" : (ORIGINS.find((o) => o.value === r.origin)?.label ?? r.origin);
    const kind = r.channel_kind ? ` ${r.channel_kind}s` : "";
    const kindOf = matchKind(r);
    const cond =
      kindOf === "contains"
        ? ` whose first message contains "${matchValue(r)}"`
        : kindOf === "regex"
          ? ` whose first message matches /${matchValue(r)}/`
          : "";
    const verb = r.enabled ? "gets a ticket" : "gets NO ticket";
    return `New chats from ${origin}${kind}${cond} → ${verb}.`;
  }
</script>

<div class="space-y-4">
  <!-- The section card supplies the title and description; this row is just
       the switch plus a one-line state read-out. -->
  <div class="flex items-center justify-between gap-3 rounded-lg border border-white-300 bg-white-200 px-4 py-3 dark:border-navy-600 dark:bg-navy-800">
    <span class="text-sm font-medium text-black-900 dark:text-white-100">
      {enabled ? "Ticket mode is on" : "Ticket mode is off"}
    </span>
    <button
      type="button"
      role="switch"
      aria-checked={enabled}
      aria-label="Enable ticket system"
      onclick={() => patch({ enabled: !enabled })}
      class={"relative h-6 w-11 shrink-0 rounded-full transition-colors " + (enabled ? "bg-green-500" : "bg-white-400 dark:bg-navy-600")}
    >
      <span class={"absolute top-0.5 h-5 w-5 rounded-full bg-white-100 shadow-sm transition-all " + (enabled ? "left-[22px]" : "left-0.5")}></span>
    </button>
  </div>

  {#if enabled}
    <!-- Field schema.

         Each field is its own bordered row rather than a cell in one wide
         grid: the column-header + free-grid version drifted out of
         alignment (the remove button sat outside its own column) and had
         nowhere sensible to wrap on a narrow viewport. A row carries its
         own labels, so nothing has to line up with a header that is not
         there. -->
    <div class="space-y-2">
      <p class="text-xs font-medium text-black-800 dark:text-black-600">Custom fields</p>

      {#each fields as f, i (i)}
        <div class="rounded-lg border border-white-300 bg-white-200 p-2.5 dark:border-navy-600 dark:bg-navy-800">
          <div class="flex items-center gap-2">
            <!-- One row per field on desktop, stacking on narrow screens.
                 Options only occupies a column when the type can use it,
                 so a text field does not leave a dead input behind. -->
            <div
              class={"grid min-w-0 flex-1 gap-2 " +
                (f.type === "select"
                  ? "sm:grid-cols-[1fr_1fr_96px_1.4fr_auto]"
                  : "sm:grid-cols-[1fr_1fr_96px_auto]")}
            >
              <input
                value={f.key}
                placeholder="key"
                aria-label="Field key"
                oninput={(e) => patchField(i, { key: (e.target as HTMLInputElement).value })}
                class="w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 font-mono text-xs text-black-900 outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
              />
              <input
                value={f.label}
                placeholder="Label"
                aria-label="Field label"
                oninput={(e) => patchField(i, { label: (e.target as HTMLInputElement).value })}
                class="w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
              />
              <select
                value={f.type}
                aria-label="Field type"
                onchange={(e) => patchField(i, { type: (e.target as HTMLSelectElement).value as "text" | "select" })}
                class="w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
              >
                <option value="text">text</option>
                <option value="select">select</option>
              </select>
              {#if f.type === "select"}
                <input
                  value={(f.options ?? []).join(", ")}
                  placeholder="low, normal, high"
                  aria-label="Field options, comma-separated"
                  oninput={(e) => setOptions(i, (e.target as HTMLInputElement).value)}
                  class="w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
                />
              {/if}
              <label class="flex cursor-pointer items-center gap-1.5 whitespace-nowrap text-[11px] text-black-800 dark:text-black-600">
                <input
                  type="checkbox"
                  checked={f.required === true}
                  aria-label="Field required"
                  onchange={(e) => patchField(i, { required: (e.target as HTMLInputElement).checked })}
                  class="h-3.5 w-3.5 rounded border-white-400 text-green-600 focus:ring-green-500 dark:border-navy-600"
                />
                Req.
              </label>
            </div>
            <button
              type="button"
              aria-label="Remove field"
              onclick={() => removeField(i)}
              class="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-neg-100 hover:text-neg-400 dark:text-black-600"
            >
              <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path>
              </svg>
            </button>
          </div>
        </div>
      {/each}

      {#if fields.length === 0}
        <p class="text-xs text-black-700 dark:text-black-600">
          No custom fields. Status and assignee are built in and always available.
        </p>
      {/if}

      <button
        type="button"
        onclick={addField}
        class="rounded-lg border border-green-500 px-3 py-1.5 text-xs font-medium text-green-600 transition-colors hover:bg-green-50 dark:text-green-400 dark:hover:bg-green-900/20"
      >+ Add field</button>
    </div>

    <!-- Automation -->
    <div class="grid gap-4 sm:grid-cols-2">
      <div>
        <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="tkt-followup">
          Follow up when stale (minutes, 0 = off)
        </label>
        <input
          id="tkt-followup"
          type="number"
          min="0"
          value={followupMin}
          oninput={(e) => patch({ followup_after_sec: Math.max(0, Number((e.target as HTMLInputElement).value) || 0) * 60 })}
          class="mt-1 w-32 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
        />
      </div>
      <div>
        <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="tkt-resolve">
          Auto-resolve when idle (days, 0 = off)
        </label>
        <input
          id="tkt-resolve"
          type="number"
          min="0"
          step="0.5"
          value={resolveDays}
          oninput={(e) => patch({ auto_resolve_after_sec: Math.round(Math.max(0, Number((e.target as HTMLInputElement).value) || 0) * 86400) })}
          class="mt-1 w-32 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
        />
      </div>
    </div>

    <div>
      <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="tkt-prompt">
        Follow-up prompt
      </label>
      <textarea
        id="tkt-prompt"
        rows="3"
        value={cfg.followup_prompt ?? ""}
        placeholder="Check this ticket's latest state, post a short update to the ops channel, and update the ticket status."
        oninput={(e) => patch({ followup_prompt: (e.target as HTMLTextAreaElement).value })}
        class="mt-1 w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:outline-none"
      ></textarea>
      <p class="mt-1 text-[11px] leading-relaxed text-black-700 dark:text-black-600">
        When a ticket goes stale, the session's agent is woken with this prompt and
        decides what to do (escalate to a channel, ping the assignee, close it).
        Not a message to the user — an instruction to the agent.
      </p>
    </div>

    <!-- Auto-create rules. Order is meaning: the first match decides, so a
         disabled narrow rule above a broad one is how an exception is
         written ("all of Slack, except DMs"). -->
    <div class="space-y-2">
      <div>
        <p class="text-xs font-medium text-black-800 dark:text-black-600">Create tickets automatically</p>
        <p class="mt-1 text-[11px] leading-relaxed text-black-700 dark:text-black-600">
          Rules are checked in order and the <strong>first match wins</strong>. Put a narrow rule
          above a broad one and switch it off to carve out an exception — a disabled
          <em>Slack&nbsp;DM</em> rule above an enabled <em>Slack</em> rule means "all of Slack
          except DMs". No rules means nothing is created automatically.
        </p>
      </div>

      {#each rules as r, i (i)}
        {@const rxErr = regexError(r)}
        <div class="rounded-lg border border-white-300 bg-white-200 p-2.5 dark:border-navy-600 dark:bg-navy-800">
          <div class="flex items-start gap-2">
            <div class="grid min-w-0 flex-1 gap-2 sm:grid-cols-[1fr_1fr_110px_1.4fr]">
              <label class="block">
                <span class="mb-1 block text-[10px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600">From</span>
                <select
                  value={r.origin}
                  onchange={(e) => patchRule(i, { origin: (e.target as HTMLSelectElement).value })}
                  class="w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
                >
                  {#each ORIGINS as o (o.value)}
                    <option value={o.value}>{o.label}</option>
                  {/each}
                </select>
              </label>

              <label class="block">
                <span class="mb-1 block text-[10px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600">Where</span>
                <select
                  value={r.channel_kind ?? ""}
                  onchange={(e) => patchRule(i, { channel_kind: (e.target as HTMLSelectElement).value })}
                  class="w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
                >
                  <option value="">Anywhere</option>
                  <option value="dm">DMs only</option>
                  <option value="channel">Channels only</option>
                  <option value="thread">Threads only</option>
                </select>
              </label>

              <label class="block">
                <span class="mb-1 block text-[10px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600">Condition</span>
                <select
                  value={matchKind(r)}
                  onchange={(e) => setMatchKind(i, (e.target as HTMLSelectElement).value)}
                  class="w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
                >
                  <option value="any">Always</option>
                  <option value="contains">Contains</option>
                  <option value="regex">Regex</option>
                </select>
              </label>

              <label class="block">
                <span class="mb-1 block text-[10px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600">
                  {matchKind(r) === "any" ? "—" : "Text tested against the first message"}
                </span>
                <input
                  value={matchValue(r)}
                  disabled={matchKind(r) === "any"}
                  placeholder={matchKind(r) === "regex" ? "^BUG-\\d+" : "bug"}
                  aria-label="Match value"
                  oninput={(e) => setMatchValue(i, (e.target as HTMLInputElement).value)}
                  class={[
                    "w-full rounded-lg border bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none disabled:opacity-40 dark:bg-navy-700 dark:text-white-100",
                    rxErr
                      ? "border-neg-400 focus:border-neg-400"
                      : "border-white-400 focus:border-green-500 dark:border-navy-600",
                  ].join(" ")}
                />
              </label>
            </div>

            <div class="mt-5 flex shrink-0 items-center gap-1">
              <button
                type="button"
                aria-label="Move rule up"
                title="Earlier rules win"
                disabled={i === 0}
                onclick={() => moveRule(i, -1)}
                class="flex h-7 w-7 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-white-300 disabled:opacity-30 dark:text-black-600 dark:hover:bg-navy-600"
              >
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M4 10l4-4 4 4" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              </button>
              <button
                type="button"
                aria-label="Move rule down"
                disabled={i === rules.length - 1}
                onclick={() => moveRule(i, 1)}
                class="flex h-7 w-7 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-white-300 disabled:opacity-30 dark:text-black-600 dark:hover:bg-navy-600"
              >
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              </button>
              <button
                type="button"
                aria-label="Remove rule"
                onclick={() => removeRule(i)}
                class="flex h-7 w-7 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-neg-100 hover:text-neg-400 dark:text-black-600"
              >
                <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path>
                </svg>
              </button>
            </div>
          </div>

          <div class="mt-2 flex flex-wrap items-center gap-3">
            <label class="flex cursor-pointer items-center gap-1.5 text-[11px] text-black-800 dark:text-black-600">
              <input
                type="checkbox"
                checked={r.enabled}
                aria-label="Rule creates a ticket"
                onchange={(e) => patchRule(i, { enabled: (e.target as HTMLInputElement).checked })}
                class="h-3.5 w-3.5 rounded border-white-400 text-green-600 focus:ring-green-500 dark:border-navy-600"
              />
              Creates a ticket
            </label>
            <input
              value={r.title ?? ""}
              placeholder={"Title template — {message} / {origin}"}
              aria-label="Ticket title template"
              oninput={(e) => patchRule(i, { title: (e.target as HTMLInputElement).value })}
              class="min-w-0 flex-1 rounded-lg border border-white-400 bg-white-100 px-2 py-1 text-[11px] text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
            />
          </div>

          <p class={"mt-2 text-[11px] " + (rxErr ? "text-neg-400" : "text-black-700 dark:text-black-600")}>
            {rxErr ? `Regex will be rejected: ${rxErr}` : ruleSummary(r)}
          </p>
        </div>
      {/each}

      {#if rules.length === 0}
        <p class="text-xs text-black-700 dark:text-black-600">
          No rules — tickets are only created by hand or by an agent.
        </p>
      {/if}

      <button
        type="button"
        onclick={addRule}
        class="rounded-lg border border-green-500 px-3 py-1.5 text-xs font-medium text-green-600 transition-colors hover:bg-green-50 dark:text-green-400 dark:hover:bg-green-900/20"
      >+ Add rule</button>
    </div>
  {/if}
</div>
