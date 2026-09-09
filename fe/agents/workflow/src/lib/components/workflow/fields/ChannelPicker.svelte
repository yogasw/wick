<script lang="ts">
  // ChannelPicker — the Channel row in the node + trigger inspectors.
  //
  // A native <select> can only render one line of text, which is why the
  // first version had to cram everything into "Slack - Ygsw - Yoga
  // Setiawan": unreadable, and worse once a bot or a person has a long
  // name. This is a listbox instead, so each row is
  //
  //     Slack                       ← what the node stores
  //     Ygsw · Yoga Setiawan        ← which bot, whose channel row
  //
  // and two instances of the same channel type are told apart at a
  // glance rather than reading "Slack" twice.
  //
  // Scope: your own instances, plus whatever the node already pinned
  // (someone else's bot stays visible instead of silently vanishing from
  // the row that uses it), plus every instance of a channel type you own
  // nothing of — otherwise the picker would be empty.
  import type { ChannelDescriptor } from "$lib/api/workflow";

  type Props = {
    label?: string;
    channels: ChannelDescriptor[];
    /** Channel type currently selected (what the node stores). */
    value: string;
    /** Instance key currently pinned, if any. */
    instance?: string;
    onChange: (channel: string, instance: string) => void;
    helper?: string;
  };

  let {
    label = "Channel",
    channels,
    value,
    instance = "",
    onChange,
    helper,
  }: Props = $props();

  let open = $state(false);

  const options = $derived.by(() => {
    const ownedTypes = new Set(
      channels.filter((c) => c.mine).map((c) => c.name),
    );
    return channels.filter(
      (c) =>
        c.mine ||
        !ownedTypes.has(c.name) ||
        (!!instance && c.instance_key === instance),
    );
  });

  const selected = $derived.by(() => {
    if (!value) return undefined;
    if (instance) {
      const pinned = options.find((c) => c.instance_key === instance);
      if (pinned) return pinned;
    }
    return options.find((c) => c.name === value);
  });

  function subtitle(c: ChannelDescriptor): string {
    return [c.bot_name, c.owner_name].filter(Boolean).join(" · ");
  }

  function pick(c: ChannelDescriptor) {
    open = false;
    onChange(c.name, c.instance_key ?? "");
  }

  function clear() {
    open = false;
    onChange("", "");
  }
</script>

<div class="flex flex-col gap-1">
  <span class="text-xs font-medium text-black-700 dark:text-black-500">{label}</span>

  <div class="relative">
    <button
      type="button"
      class="w-full flex items-center justify-between gap-2 rounded-lg border border-white-400 dark:border-navy-600
             bg-white-100 dark:bg-navy-800 px-3 py-2 text-left transition-colors
             hover:border-white-500 dark:hover:border-navy-500 focus:outline-none focus:border-green-500"
      onclick={() => (open = !open)}
      aria-haspopup="listbox"
      aria-expanded={open}
    >
      <span class="min-w-0 flex flex-col">
        {#if selected}
          <span class="text-sm font-medium truncate text-black-900 dark:text-white-100">
            {selected.label || selected.name}
          </span>
          {#if subtitle(selected)}
            <span class="text-[11px] leading-snug truncate text-black-700 dark:text-black-500">
              {subtitle(selected)}
            </span>
          {/if}
        {:else}
          <span class="text-sm text-black-700 dark:text-black-500">Select a channel…</span>
        {/if}
      </span>
      <span class="text-black-700 dark:text-black-500 shrink-0" aria-hidden="true">▾</span>
    </button>

    {#if open}
      <!-- Click-away layer: closes the list without stealing the click
           target's own handler on the row above. -->
      <button
        type="button"
        class="fixed inset-0 z-10 cursor-default"
        aria-label="Close channel list"
        onclick={() => (open = false)}
      ></button>

      <div
        class="absolute z-20 mt-1 w-full max-h-64 overflow-y-auto rounded-lg border border-white-400 dark:border-navy-600
               bg-white-100 dark:bg-navy-800 shadow-xl py-1"
        role="listbox"
      >
        <button
          type="button"
          class="w-full px-3 py-2 text-left text-sm text-black-700 dark:text-black-500
                 hover:bg-white-200 dark:hover:bg-navy-700"
          onclick={clear}
        >
          (none)
        </button>
        {#each options as c (c.instance_key || c.name)}
          {@const active =
            c.name === value && (!instance || c.instance_key === instance)}
          <button
            type="button"
            role="option"
            aria-selected={active}
            class="w-full flex items-center justify-between gap-2 px-3 py-2 text-left transition-colors
                   {active
              ? 'bg-emerald-50 dark:bg-emerald-950/30'
              : 'hover:bg-white-200 dark:hover:bg-navy-700'}"
            onclick={() => pick(c)}
          >
            <span class="min-w-0 flex flex-col">
              <span class="text-sm font-medium truncate text-black-900 dark:text-white-100">
                {c.label || c.name}
              </span>
              {#if subtitle(c)}
                <span class="text-[11px] leading-snug truncate text-black-700 dark:text-black-500">
                  {subtitle(c)}
                </span>
              {/if}
            </span>
            {#if c.mine}
              <span class="text-[10px] shrink-0 rounded px-1.5 py-0.5 bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-400">
                you
              </span>
            {/if}
          </button>
        {/each}
        {#if options.length === 0}
          <p class="px-3 py-3 text-xs italic text-black-700 dark:text-black-600">
            No channel configured yet.
          </p>
        {/if}
      </div>
    {/if}
  </div>

  {#if helper}
    <span class="text-[11px] text-black-700 dark:text-black-600">{helper}</span>
  {/if}
</div>
