<script lang="ts">
  /* The project's own actions, in one "⋯".

     They used to be a bar of their own above the board: a breadcrumb back to
     All chats, the project's name, its chat count, and two buttons. That row
     said almost nothing a person looking at their own board did not already
     know, and cost a full row of height on every view — on a board, height is
     the scarce dimension.

     So the label goes and only the ACTIONS stay, folded behind one 28px
     button that rides in the toolbar that was already there. Leaving is one
     of those actions rather than a permanent link: it is the rarest of the
     three, and a menu is a fine place for "get me out of here". */
  import { DeleteProjectDialog } from "@wick-fe/common-ui";

  type Props = {
    base: string;
    project: { id: string; name: string; path?: string; pinned?: boolean; managed?: boolean };
    /* How many chats the project holds — shown in the menu's header, which is
       where the old bar's "491 chats · managed" suffix ended up. */
    chatCount?: number;
    onPin: () => void;
  };

  let { base, project, chatCount, onPin }: Props = $props();

  let open = $state(false);
  let confirmDelete = $state(false);
</script>

<DeleteProjectDialog
  open={confirmDelete}
  {base}
  projectID={project.id}
  onDeleted={() => { confirmDelete = false; window.location.href = `${base}/sessions`; }}
  onCancel={() => { confirmDelete = false; }}
/>

<div class="relative shrink-0">
  <button
    type="button"
    aria-haspopup="menu"
    aria-expanded={open}
    aria-label="Project actions"
    title={project.path || project.name}
    data-testid="project-menu"
    onclick={() => { open = !open; }}
    class="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-white-400 bg-white-100 text-black-700 transition-colors hover:bg-white-200 dark:border-navy-600 dark:bg-navy-700 dark:text-black-600 dark:hover:bg-navy-600"
  >
    <svg viewBox="0 0 16 16" class="h-4 w-4" fill="currentColor" aria-hidden="true">
      <circle cx="3.5" cy="8" r="1.25"></circle>
      <circle cx="8" cy="8" r="1.25"></circle>
      <circle cx="12.5" cy="8" r="1.25"></circle>
    </svg>
    {#if project.pinned}
      <!-- The one piece of state worth seeing without opening the menu: a
           pinned project is where every "go to wick" lands, and forgetting
           which one that is explains a lot of confusion. -->
      <span
        aria-hidden="true"
        class="absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full border border-white-100 bg-green-500 dark:border-navy-700"
      ></span>
    {/if}
  </button>

  {#if open}
    <!-- Click-away, so the menu never strands the toolbar half-open. -->
    <button
      type="button"
      tabindex="-1"
      aria-label="Close menu"
      onclick={() => { open = false; }}
      class="fixed inset-0 z-20 cursor-default"
    ></button>
    <div
      role="menu"
      class="absolute right-0 top-9 z-30 w-56 overflow-hidden rounded-lg border border-white-300 bg-white-100 py-1 shadow-lg dark:border-navy-600 dark:bg-navy-800"
    >
      <div class="border-b border-white-300 px-3 py-2 dark:border-navy-600">
        <p class="truncate text-xs font-semibold text-black-900 dark:text-white-100" title={project.path || project.name}>
          {project.name}
        </p>
        <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">
          {chatCount ?? 0} chats · {project.managed ? "managed" : "custom"}
        </p>
      </div>

      <button
        type="button"
        role="menuitem"
        data-testid="project-menu-pin"
        onclick={() => { open = false; onPin(); }}
        class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-black-800 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-700"
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M6 2h4l-.5 4 2.5 2.5H4L6.5 6 6 2z" stroke-linejoin="round"></path>
          <path d="M8 8.5V14" stroke-linecap="round"></path>
        </svg>
        {project.pinned ? "Unpin as default" : "Pin as default"}
      </button>

      <a
        role="menuitem"
        href={`${base}/projects/${project.id}`}
        class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-black-800 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-700"
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <circle cx="8" cy="8" r="6"></circle>
          <path d="M8 5v3l2 2" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
        Project settings
      </a>

      <!-- The server refuses a protected, someone else's or already
           converted project; the wizard shows why. -->
      <a
        role="menuitem"
        data-testid="project-menu-make-agent"
        href={`${base}/team?panel=new&project=${encodeURIComponent(project.id)}`}
        class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-black-800 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-700"
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <circle cx="8" cy="6" r="2.5"></circle>
          <path d="M3.5 13.5c.6-2.3 2.4-3.5 4.5-3.5s3.9 1.2 4.5 3.5" stroke-linecap="round"></path>
        </svg>
        Make this an agent…
      </a>

      <a
        role="menuitem"
        href={`${base}/sessions`}
        class="flex w-full items-center gap-2 border-t border-white-300 px-3 py-1.5 text-left text-xs text-black-800 transition-colors hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-700"
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
        All chats
      </a>

      <!-- Last and red: the one action here that cannot be taken back. The
           dialog says what goes and refuses a protected project itself. -->
      <button
        type="button"
        role="menuitem"
        data-testid="project-menu-delete"
        onclick={() => { open = false; confirmDelete = true; }}
        class="flex w-full items-center gap-2 border-t border-white-300 px-3 py-1.5 text-left text-xs text-neg-400 transition-colors hover:bg-neg-100 dark:border-navy-600"
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M3 4.5h10M6.5 4.5V3h3v1.5M4.5 4.5l.5 8.5h6l.5-8.5" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
        Delete project…
      </button>
    </div>
  {/if}
</div>
