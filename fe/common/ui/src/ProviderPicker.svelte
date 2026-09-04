<script lang="ts">
  /* Standalone provider picker: a dropdown that descends provider TYPE ▸
     INSTANCE ▸ MODEL, each level collapsing when it has only one choice.
     Same nesting the Composer's + menu uses, but usable anywhere a plain
     <select> was (project defaults, settings). Value is "type/name" or
     "type/name::modelID". */
  import type { ComposerModelOption, ComposerSelectOption } from "./composer-types.js";

  type Props = {
    options: ComposerSelectOption[];
    value: string;
    onChange: (v: string) => void;
    /** Shown when value matches nothing (e.g. a deleted instance). */
    placeholder?: string;
    id?: string;
    /** Lazy model loader, same contract as ComposerSelect.loadModels: called
        with an option's value when drilling into it, and again with
        `{entry}` to expand a LIVE SET row into the vendor models it holds
        (the 4th level). Without it, this picker can only offer the models
        baked into `options` — which for wick means it cannot reach a live
        set's leaves at all. */
    loadModels?: (
      optionValue: string,
      opts?: { entry?: string },
    ) => Promise<ComposerModelOption[]>;
  };
  let { options, value, onChange, placeholder = "Select provider", id, loadModels }: Props = $props();

  let open = $state(false);
  let typeDrill = $state<string>(""); // level 2: instances of this type
  let modelDrill = $state<ComposerSelectOption | null>(null); // level 3
  let rootEl = $state<HTMLDivElement | undefined>();
  let triggerEl = $state<HTMLButtonElement | undefined>();
  let menuEl = $state<HTMLDivElement | undefined>();
  let modelSearch = $state("");
  let modelSearchEl = $state<HTMLInputElement | undefined>();

  /* The popup is position:fixed, anchored to the trigger via
     getBoundingClientRect, so it escapes any scrolling/overflow ancestor.
     Inside a Modal — whose body is overflow-auto — an absolutely positioned
     menu was clipped by the body and forced it to scroll, which is the bug
     this replaces. Same approach KebabMenu already uses. */
  let pos = $state<{ top: number; left: number; width: number } | null>(null);
  const GAP = 4;
  const PAD = 8;
  /* Cap + estimate used to pick a flip direction before the menu has
     rendered; the $effect below re-places with the measured height. */
  const MAX_H = 320;

  function place() {
    if (!triggerEl) return;
    const r = triggerEl.getBoundingClientRect();
    // Match the trigger's width so the menu reads as part of the control.
    const width = Math.max(r.width, 256);
    let left = r.left;
    if (left + width > window.innerWidth - PAD) left = window.innerWidth - PAD - width;
    if (left < PAD) left = PAD;

    const menuH = Math.min(menuEl?.offsetHeight ?? MAX_H, MAX_H);
    const below = r.bottom + GAP;
    const fitsBelow = below + menuH <= window.innerHeight - PAD;
    let top = fitsBelow ? below : r.top - GAP - menuH;
    if (top < PAD) top = PAD;
    if (top + menuH > window.innerHeight - PAD) {
      top = Math.max(PAD, window.innerHeight - PAD - menuH);
    }
    pos = { top, left, width };
  }

  // Loaded model lists, keyed by option value for level 3 and by
  // "value + entry" for a live set's expansion. Cached so re-opening the
  // menu does not refetch, and so the vendor is not asked again for a list
  // the user is still looking at.
  let modelCache = $state<Record<string, ComposerModelOption[]>>({});
  let loadingModels = $state(false);
  // The live set currently expanded (level 4), or null at level 3.
  let setDrill = $state<ComposerModelOption | null>(null);

  function cacheKey(optionValue: string, entry?: string): string {
    return entry ? `${optionValue}::${entry}` : optionValue;
  }

  // Filter the drilled instance's models by the search box. Same tiny grammar
  // as elsewhere: contains by default, `-`/`!` prefix excludes.
  function modelMatches(m: { id: string; label: string }, q: string): boolean {
    const hay = `${m.id} ${m.label}`.toLowerCase();
    for (const raw of q.toLowerCase().split(/\s+/)) {
      const t = raw.trim();
      if (t === "" || t === "-" || t === "!") continue;
      const exclude = t.startsWith("-") || t.startsWith("!");
      const needle = exclude ? t.slice(1) : t;
      const hit = hay.includes(needle);
      if (exclude ? hit : !hit) return false;
    }
    return true;
  }
  // The list to render: a live set's expansion when one is open (level 4),
  // else the drilled option's models — loaded ones preferred over the static
  // list, since the loaded ones came from the vendor just now.
  const drillModels = $derived.by(() => {
    const d = modelDrill;
    if (!d) return [];
    const all = setDrill
      ? (modelCache[cacheKey(d.value, setDrill.id)] ?? [])
      : (modelCache[cacheKey(d.value)] ?? d.models ?? []);
    const q = modelSearch.trim();
    return q ? all.filter((m) => modelMatches(m, q)) : all;
  });

  // Re-place once the popup exists (real height), and again whenever the level
  // changes — drilling in or out changes the height, and a stale position
  // would leave the menu hanging off the trigger it belongs to.
  $effect(() => {
    if (!open) return;
    // Read the level state so this re-runs on every drill.
    void modelDrill;
    void typeDrill;
    void setDrill;
    void drillModels.length;
    if (menuEl) place();
  });

  // Fetch a level's models unless they are already cached. Errors are
  // swallowed: the static list (or an empty one) is a usable fallback, and a
  // provider whose vendor is unreachable must not break the whole form.
  async function ensureModels(optionValue: string, entry?: string) {
    if (!loadModels) return;
    const key = cacheKey(optionValue, entry);
    if (modelCache[key]) return;
    loadingModels = true;
    try {
      const loaded = await loadModels(optionValue, entry ? { entry } : undefined);
      if (loaded && loaded.length > 0) modelCache = { ...modelCache, [key]: loaded };
    } catch {
      // keep whatever static models the option already carries
    } finally {
      loadingModels = false;
    }
  }

  function isLiveSet(m: ComposerModelOption): boolean {
    return !!m.live;
  }

  // Open a live-set row, or collapse it when it is already open.
  function toggleSetDrill(m: ComposerModelOption) {
    const d = modelDrill;
    if (!d) return;
    if (setDrill?.id === m.id) {
      setDrill = null;
      modelSearch = "";
      return;
    }
    setDrill = m;
    modelSearch = "";
    void ensureModels(d.value, m.id);
  }

  // Focus the search box the moment we drill into a model list (only shown
  // when the list is long enough to be worth filtering).
  $effect(() => {
    if (modelDrill && modelSearchEl) modelSearchEl.focus();
  });

  function splitPin(v: string): { key: string; modelID: string } {
    const i = v.indexOf("::");
    return i < 0 ? { key: v, modelID: "" } : { key: v.slice(0, i), modelID: v.slice(i + 2) };
  }
  function rawType(v: string): string {
    const key = splitPin(v).key;
    const s = key.indexOf("/");
    return s < 0 ? key : key.slice(0, s);
  }
  // Worth drilling when the option ships more than one model, OR when a
  // loader is wired — in the latter case the real list is not known until it
  // is fetched, and refusing to drill would hide every live model behind an
  // instance that looks single-model.
  function hasModels(o: ComposerSelectOption): boolean {
    if (loadModels) return true;
    return !!o.models && o.models.length > 1;
  }

  const groups = $derived.by(() => {
    const gs: { type: string; opts: ComposerSelectOption[] }[] = [];
    const idx = new Map<string, number>();
    for (const o of options) {
      const t = rawType(o.value);
      let i = idx.get(t);
      if (i === undefined) { i = gs.length; idx.set(t, i); gs.push({ type: t, opts: [] }); }
      gs[i].opts.push(o);
    }
    return gs;
  });

  // Label of the current value: "claude · opus" when a model is pinned.
  //
  // A live-set pin is "<entryID>@<vendorModelID>", which matches no entry in
  // the static list, so the vendor half is shown directly rather than falling
  // back to the bare instance name — otherwise a project pinned to a specific
  // leaf would read as if no model were chosen at all.
  const label = $derived.by(() => {
    const { key, modelID } = splitPin(value);
    const opt = options.find((o) => o.value === key);
    if (!opt) return value || placeholder;
    if (!modelID) return opt.label;
    const at = modelID.indexOf("@");
    if (at >= 0) return `${opt.label} · ${modelID.slice(at + 1)}`;
    const m = opt.models?.find((x) => x.id === modelID);
    // A loaded list can name an id the static options do not carry: the
    // provider list collapses a single-model instance to no models at all, so
    // without this the closed trigger shows a bare id like "m_0370951f-68d".
    const loaded = modelCache[cacheKey(opt.value)]?.find((x) => x.id === modelID);
    const name = m?.label || loaded?.label || modelID;
    return `${opt.label} · ${name}`;
  });

  /* Resolve the current pin's name once on mount when the static options
     cannot, so the closed trigger reads as a model name rather than an id.
     Only for an id-shaped pin (no "@" — a live-set leaf is already named). */
  $effect(() => {
    if (!loadModels) return;
    const { key, modelID } = splitPin(value);
    if (!key || !modelID || modelID.includes("@")) return;
    const opt = options.find((o) => o.value === key);
    if (!opt || opt.models?.some((x) => x.id === modelID)) return;
    if (modelCache[cacheKey(opt.value)]) return;
    void ensureModels(opt.value);
  });

  function reset() { typeDrill = ""; modelDrill = null; setDrill = null; modelSearch = ""; }
  function close() { open = false; reset(); }
  // Back out one level: from a live set's expansion to the model list, then
  // from the model list to the instances. Clearing the filter each time, so a
  // stale query cannot hide the level you just returned to.
  function backFromModels() {
    if (setDrill) { setDrill = null; modelSearch = ""; return; }
    modelDrill = null;
    modelSearch = "";
  }

  function drillModel(o: ComposerSelectOption) {
    modelSearch = "";
    setDrill = null;
    modelDrill = o;
    void ensureModels(o.value);
  }

  function pickType(g: { type: string; opts: ComposerSelectOption[] }) {
    if (g.opts.length === 1) {
      const only = g.opts[0];
      if (hasModels(only)) { drillModel(only); return; }
      onChange(only.value); close();
      return;
    }
    typeDrill = g.type;
  }
  function pickInstance(o: ComposerSelectOption) {
    if (hasModels(o)) { drillModel(o); return; }
    onChange(o.value); close();
  }
  // A leaf chosen inside a live set is addressed as "<entryID>@<vendorModelID>":
  // the entry supplies the key/kind/base, the vendor id the concrete model. A
  // plain model is its own id.
  function pickModel(o: ComposerSelectOption, modelID: string) {
    const packed = setDrill ? `${setDrill.id}@${modelID}` : modelID;
    onChange(`${o.value}::${packed}`);
    close();
  }

  $effect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      const t = e.target as Node;
      // The menu is no longer a DOM child of rootEl (it is fixed-positioned
      // at the document level), so it has to be checked separately or every
      // click inside the menu would close it.
      if (rootEl?.contains(t)) return;
      if (menuEl?.contains(t)) return;
      close();
    }
    function onKey(e: KeyboardEvent) { if (e.key === "Escape") close(); }
    // A fixed popup does not travel with its trigger, so re-anchor it when
    // anything moves underneath. Capture phase catches scrolls on inner
    // containers, not just the window.
    function onReflow() { place(); }
    window.addEventListener("mousedown", onDown, true);
    window.addEventListener("keydown", onKey);
    window.addEventListener("scroll", onReflow, true);
    window.addEventListener("resize", onReflow);
    return () => {
      window.removeEventListener("mousedown", onDown, true);
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("scroll", onReflow, true);
      window.removeEventListener("resize", onReflow);
    };
  });
</script>

<div bind:this={rootEl}>
  <button
    {id}
    bind:this={triggerEl}
    type="button"
    onclick={() => { if (open) close(); else { reset(); open = true; place(); } }}
    class="flex w-full items-center justify-between gap-2 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 hover:border-green-500 transition-colors"
  >
    <span class="truncate">{label}</span>
    <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"/></svg>
  </button>

  {#if open && pos}
    <!-- Fixed, not absolute: an absolute popup is clipped by any
         overflow-auto ancestor (a Modal body, a scrolling settings pane) and
         makes that ancestor scroll instead of painting over it. z above the
         Modal's own layer so it is never covered by the dialog it sits in. -->
    <div
      bind:this={menuEl}
      style="position:fixed; top:{pos.top}px; left:{pos.left}px; width:{pos.width}px; max-height:{MAX_H}px; z-index:9999;"
      class="flex flex-col overflow-hidden rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-xl"
    >
      {#if modelDrill}
        {@const d = modelDrill}
        <button type="button" onclick={backFromModels} class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-semibold text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700">
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
          {setDrill ? `${d.label} · ${setDrill.label}` : d.label}
        </button>
        <div class="border-t border-white-300 dark:border-navy-600"></div>
        <!-- Filter box: worth showing once the list is long enough to scan. -->
        {#if drillModels.length > 6 || modelSearch}
          <div class="px-2 pt-2">
            <input
              type="text"
              bind:this={modelSearchEl}
              bind:value={modelSearch}
              placeholder="Filter models…"
              class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2.5 py-1.5 text-xs text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500"
            />
          </div>
        {/if}
        <!-- Flexes inside the menu's own cap rather than adding a second
             limit: two stacked max-heights produce a scrollbar inside a
             scrollbar. -->
        <div class="min-h-0 flex-1 overflow-y-auto py-1">
          {#each drillModels as m (m.id)}
            <!-- A live-set row is not selectable: it names a SET, so it opens
                 one level deeper instead of committing. Inside a set, rows are
                 vendor models and pack as "<entryID>@<vendorModelID>". -->
            {@const live = !setDrill && isLiveSet(m)}
            {@const pinned = `${d.value}::${setDrill ? `${setDrill.id}@${m.id}` : m.id}`}
            {@const isSel = !live && (value === pinned || (value === d.value && m.default))}
            <button
              type="button"
              onclick={() => { if (live) toggleSetDrill(m); else pickModel(d, m.id); }}
              class="flex w-full items-start justify-between gap-3 px-3 py-1.5 text-left {isSel ? 'bg-green-500/10' : 'hover:bg-white-200 dark:hover:bg-navy-700'} text-black-900 dark:text-white-100"
            >
              <span class="flex flex-col min-w-0">
                <span class="flex items-center gap-2 text-sm">
                  <span class="truncate">{m.label}</span>
                  {#if live}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">live set</span>
                  {:else if m.default}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">default</span>{/if}
                </span>
                {#if m.desc}<span class="text-[11px] text-black-700 dark:text-black-600 leading-snug">{m.desc}</span>{/if}
              </span>
              {#if live}
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 mt-0.5 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
              {:else if isSel}<span class="shrink-0 mt-0.5 text-green-600 dark:text-green-400">✓</span>{/if}
            </button>
          {:else}
            <div class="px-3 py-3 text-xs text-black-700 dark:text-black-600">
              {#if loadingModels}Loading models…
              {:else if modelSearch}No models match “{modelSearch}”.
              {:else}No models available.{/if}
            </div>
            <!-- Escape hatch: an instance whose loader yields nothing would
                 otherwise be unselectable — the drill is forced whenever a
                 loader is wired, and there is no model row to commit through. -->
            {#if !loadingModels && !modelSearch && !setDrill}
              <button
                type="button"
                onclick={() => { onChange(d.value); close(); }}
                class="flex w-full items-center px-3 py-1.5 text-left text-sm text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700"
              >Use default model</button>
            {/if}
          {/each}
        </div>
      {:else if typeDrill}
        {@const group = groups.find((g) => g.type === typeDrill)}
        <button type="button" onclick={() => (typeDrill = "")} class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-semibold text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700">
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
          {typeDrill}
        </button>
        <div class="border-t border-white-300 dark:border-navy-600"></div>
        <div class="min-h-0 flex-1 overflow-y-auto py-1">
          {#each group?.opts ?? [] as o (o.value)}
            {@const isSel = splitPin(value).key === o.value}
            <button type="button" onclick={() => pickInstance(o)} class="flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-sm {isSel ? 'bg-green-500/10' : 'hover:bg-white-200 dark:hover:bg-navy-700'} text-black-900 dark:text-white-100">
              <span class="flex items-center gap-2 min-w-0"><span class="truncate">{o.label}</span>{#if o.badge}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">{o.badge}</span>{/if}</span>
              {#if hasModels(o)}
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
              {:else if isSel}<span class="shrink-0 text-green-600 dark:text-green-400">✓</span>{/if}
            </button>
          {/each}
        </div>
      {:else}
        {@const selKey = splitPin(value).key}
        <div class="min-h-0 flex-1 overflow-y-auto py-1">
          {#each groups as g (g.type)}
            {@const single = g.opts.length === 1 ? g.opts[0] : null}
            {@const nested = g.opts.length > 1 || (single ? hasModels(single) : false)}
            <button type="button" onclick={() => pickType(g)} class="flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-sm {(single && single.value === selKey) ? 'bg-green-500/10' : 'hover:bg-white-200 dark:hover:bg-navy-700'} text-black-900 dark:text-white-100">
              <span class="flex items-center gap-2 min-w-0"><span class="truncate">{single ? single.label : g.type}</span>{#if single?.badge}<span class="shrink-0 rounded-full bg-green-500/10 px-1.5 py-0.5 text-[10px] font-medium text-green-600 dark:text-green-400">{single.badge}</span>{/if}</span>
              {#if nested}
                <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
              {:else if single && single.value === selKey}<span class="shrink-0 text-green-600 dark:text-green-400">✓</span>{/if}
            </button>
          {/each}
        </div>
      {/if}
    </div>
  {/if}
</div>
