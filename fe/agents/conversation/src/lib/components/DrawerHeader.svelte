<script lang="ts">
  /* Title row shared by the Agents app drawers: the agent's avatar (when
     the drawer belongs to one), title, optional subtitle, and the ✕ (Esc
     does the same, handled by the app shell). */
  import { AgentAvatar } from "@wick-fe/common-avatar";

  type Props = {
    title: string;
    subtitle?: string;
    avatar?: { shape?: string; color?: string } | null;
    /** false when the drawer draws its own rule below (Settings' tab row). */
    bordered?: boolean;
    onClose: () => void;
  };
  let { title, subtitle, avatar, bordered = true, onClose }: Props = $props();
</script>

<div class="flex items-center gap-3 px-6 pt-4 {bordered ? 'border-b border-white-300 pb-4 dark:border-navy-600' : 'pb-3'}">
  {#if avatar}<AgentAvatar shape={avatar.shape} color={avatar.color} size={36} />{/if}
  <div class="min-w-0 flex-1">
    <h2 class="truncate text-base font-semibold text-black-900 dark:text-white-100">{title}</h2>
    {#if subtitle}<p class="truncate text-xs text-black-800 dark:text-black-600">{subtitle}</p>{/if}
  </div>
  <button
    type="button"
    class="flex h-8 w-8 items-center justify-center rounded-lg text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600"
    aria-label="Tutup"
    onclick={onClose}
  >
    <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 4l8 8M12 4l-8 8"></path></svg>
  </button>
</div>
