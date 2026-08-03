<script lang="ts">
  /* One sub-agent role as a list row, shared by the global Sub-agents page
     and a project's Sub-agents tab so the two surfaces cannot drift apart.

     The row itself is the edit affordance — clicking it opens the role —
     and everything else lives behind the ⋮ menu at the right. That keeps
     one primary action per row instead of two competing buttons, and stops
     a destructive Delete from sitting permanently under the cursor.

     A LOCKED role is deliberately still clickable: opening it read-only is
     how you find out what it does. What the lock removes is Delete and the
     disable toggle, and the row says so rather than silently ignoring a
     click on a menu item. */
  import type { AgentProfile } from "@wick-fe/common-api";
  import KebabMenu from "./KebabMenu.svelte";

  type Props = {
    profile: AgentProfile;
    /** Opens the role. Called on row click and on the menu's Edit item. */
    onedit: (p: AgentProfile) => void;
    /** Flips `disabled`. Omit to hide the item (e.g. a read-only scope). */
    ontoggle?: (p: AgentProfile) => void;
    /** Asks to delete. Omit to hide the item. */
    ondelete?: (p: AgentProfile) => void;
    /** Quick provider/model change. Omit to hide the item. */
    onchangeModel?: (p: AgentProfile) => void;
    /** Marks the row as the open one in a master-detail layout. */
    selected?: boolean;
    /** Shown when this project role overrides a global one of the same key. */
    shadowsGlobal?: boolean;
    /** No write actions at all (a non-admin viewing global roles). */
    readonly?: boolean;
  };
  let {
    profile,
    onedit,
    ontoggle,
    ondelete,
    onchangeModel,
    selected = false,
    shadowsGlobal = false,
    readonly = false,
  }: Props = $props();

  // A lock is a deliberate freeze, so the menu keeps the items visible and
  // disabled rather than hiding them: an item that vanishes reads as "this
  // build cannot do it", while a disabled one reads as "not on this role".
  const frozen = $derived(readonly || profile.locked);

  const items = $derived.by(() => {
    const out: {
      label: string;
      onclick: () => void;
      danger?: boolean;
      disabled?: boolean;
    }[] = [{ label: "Edit", onclick: () => onedit(profile) }];
    if (onchangeModel) {
      out.push({
        label: "Change provider / model",
        onclick: () => onchangeModel(profile),
        disabled: frozen,
      });
    }
    if (ontoggle) {
      out.push({
        label: profile.disabled ? "Enable" : "Disable",
        onclick: () => ontoggle(profile),
        disabled: frozen,
      });
    }
    if (ondelete) {
      out.push({
        label: "Delete",
        onclick: () => ondelete(profile),
        danger: true,
        disabled: frozen,
      });
    }
    return out;
  });

  // "wick/x::set1@gemini-3-pro" reads as two facts — which instance, which
  // model — so it is split for display. The vendor half of a live-set pin is
  // what the user actually chose; the entry id is plumbing.
  const instance = $derived(profile.provider.split("::")[0] ?? "");
  const modelLabel = $derived.by(() => {
    const pinned = profile.provider.includes("::")
      ? profile.provider.slice(profile.provider.indexOf("::") + 2)
      : profile.model;
    if (!pinned) return "";
    const at = pinned.indexOf("@");
    return at >= 0 ? pinned.slice(at + 1) : pinned;
  });
</script>

<li>
  <div
    class="flex items-center gap-3 rounded-lg border px-4 py-3 transition-colors
      {selected
      ? 'border-green-500 bg-white-200 dark:bg-navy-800'
      : 'border-white-300 bg-white-100 hover:border-white-400 dark:border-navy-600 dark:bg-navy-800 dark:hover:border-navy-500'}"
  >
    <!-- The row is the edit affordance. A button (not a div+onclick) so it is
         reachable by keyboard and announced as actionable. -->
    <button
      type="button"
      class="min-w-0 flex-1 text-left"
      onclick={() => onedit(profile)}
    >
      <span class="flex items-center gap-2">
        <span class="truncate text-sm font-medium text-black-900 dark:text-white-100">
          {profile.name || profile.key}
        </span>
        {#if profile.locked}
          <span
            title="Locked — open it to read, but it cannot be edited, disabled or deleted here."
            class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium text-cau-400 ring-1 ring-cau-400/40"
          >
            Locked
          </span>
        {/if}
        {#if shadowsGlobal}
          <span
            title="This project defines a role under a global key, replacing it for sessions in this project only."
            class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium text-prog-400 ring-1 ring-prog-400/40"
          >
            Shadows global
          </span>
        {/if}
        {#if profile.disabled}
          <span
            class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium text-black-700 ring-1 ring-white-400 dark:text-black-600 dark:ring-navy-600"
          >
            Disabled
          </span>
        {/if}
      </span>

      <!-- Which provider and model this role runs on, stated on the row: it
           is the thing most often checked and previously required opening
           the editor to see. -->
      <span class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">
        <span
          class="shrink-0 rounded bg-white-200 px-1.5 py-0.5 font-mono text-[10px] text-black-800 dark:bg-navy-700 dark:text-black-600"
        >
          {instance || "no provider"}
        </span>
        {#if modelLabel}
          <span
            class="shrink-0 rounded bg-white-200 px-1.5 py-0.5 font-mono text-[10px] text-black-800 dark:bg-navy-700 dark:text-black-600"
          >
            {modelLabel}
          </span>
        {:else}
          <span class="shrink-0 text-[10px] text-black-700 dark:text-black-600">
            provider default model
          </span>
        {/if}
        {#if profile.description}
          <span class="min-w-0 truncate text-[11px] text-black-700 dark:text-black-600">
            {profile.description}
          </span>
        {/if}
      </span>
    </button>

    <div class="shrink-0">
      <KebabMenu {items} ariaLabel={`Actions for ${profile.name || profile.key}`} width={216} />
    </div>
  </div>
</li>
