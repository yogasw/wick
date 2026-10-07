<script lang="ts">
  // One row of the compare's folder tree — modelled on FileTreeNode, not
  // shared with it: that component's props are the Changes contract
  // (stage / unstage / discard / ignore over a FileChange), and a compare
  // row carries a status letter, ± counts and a single "Restore from
  // base". Bending FileTreeNode to serve both would put optional branches
  // through the one list people use every day, so the visual grammar is
  // copied — same chevron, same 12px-per-depth padding, same hover — and
  // the behaviour is its own.
  import type { TreeNode } from "$lib/tree";
  import type { CompareFile } from "$lib/api/scm";
  import type { TreeStat } from "$lib/compare-tree";
  import { isOpen } from "$lib/compare-tree";
  import Self from "$lib/components/CompareTreeNode.svelte";

  type Props = {
    node: TreeNode;
    depth: number;
    expanded: Record<string, boolean>;
    /** path -> compare row, so a leaf can find its counts. */
    files: Map<string, CompareFile>;
    /** folder path -> what its whole subtree adds up to. */
    stats: Map<string, TreeStat>;
    selectedPath: string;
    busy: boolean;
    onToggleDir: (path: string) => void;
    onSelect: (f: CompareFile) => void;
    onRestore: (f: CompareFile) => void;
  };
  let {
    node, depth, expanded, files, stats, selectedPath, busy,
    onToggleDir, onSelect, onRestore,
  }: Props = $props();

  const open = $derived(isOpen(expanded, node.path));
  const pad = $derived(`padding-left: ${depth * 12 + 8}px`);
  const stat = $derived(node.isDir ? stats.get(node.path) : undefined);
  const file = $derived(node.isDir ? undefined : files.get(node.path));
  const binary = $derived(!!file && file.additions < 0 && file.deletions < 0);

  function statusColor(s: string): string {
    const k = s.charAt(0);
    if (k === "A") return "text-green-600 dark:text-green-400";
    if (k === "D") return "text-cau-600 dark:text-cau-400";
    if (k === "R" || k === "C") return "text-blue-600 dark:text-blue-400";
    return "text-amber-600 dark:text-amber-400";
  }
</script>

{#if node.isDir}
  <div class="group flex items-center gap-1 py-1 pr-2 hover:bg-white-200 dark:hover:bg-navy-800" style={pad}>
    <button type="button" onclick={() => onToggleDir(node.path)} class="flex min-w-0 flex-1 items-center gap-1 text-left">
      <svg class={"h-3 w-3 shrink-0 text-black-600 transition-transform " + (open ? "rotate-90" : "")} fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7"/></svg>
      <span class="truncate text-xs font-medium text-black-800 dark:text-black-600" title={node.path}>{node.name}</span>
    </button>
    <!-- Folders carry their subtree's totals, so a collapsed one still
         says how much is inside it. -->
    {#if stat}
      <span class="shrink-0 text-[9px] text-black-600 dark:text-black-700">{stat.files}</span>
      <span class="shrink-0 font-mono text-[9px] text-green-600 dark:text-green-400">+{stat.additions}</span>
      <span class="shrink-0 font-mono text-[9px] text-cau-600 dark:text-cau-400">−{stat.deletions}</span>
    {/if}
  </div>
  {#if open}
    {#each node.children ?? [] as child (child.path)}
      <Self node={child} depth={depth + 1} {expanded} {files} {stats} {selectedPath} {busy} {onToggleDir} {onSelect} {onRestore} />
    {/each}
  {/if}
{:else if file}
  {@const f = file}
  <div
    data-path={f.path}
    class={"group flex items-center gap-1.5 py-1 pr-2 " + (selectedPath === f.path ? "bg-white-300 dark:bg-navy-600" : "hover:bg-white-200 dark:hover:bg-navy-800")}
    style={pad}
  >
    <!-- The folder rows above already spell the path out, so the file row
         shows the basename — which is what was unreadable when 159 long
         paths were stacked as one flat list. -->
    <button type="button" onclick={() => onSelect(f)} title={f.orig_path ? `${f.orig_path} → ${f.path}` : f.path} class="flex min-w-0 flex-1 items-center gap-1.5 text-left">
      <span class={"shrink-0 font-mono text-[10px] " + statusColor(f.status)}>{f.status}</span>
      <span class="min-w-0 flex-1 truncate text-xs font-medium text-black-900 dark:text-white-100">{node.name}</span>
      {#if binary}
        <span class="shrink-0 text-[9px] text-black-600">bin</span>
      {:else}
        <span class="shrink-0 font-mono text-[9px] text-green-600 dark:text-green-400">+{Math.max(f.additions, 0)}</span>
        <span class="shrink-0 font-mono text-[9px] text-cau-600 dark:text-cau-400">−{Math.max(f.deletions, 0)}</span>
      {/if}
    </button>
    <button
      type="button"
      onclick={() => onRestore(f)}
      disabled={busy}
      title="Restore this file from the base ref"
      aria-label={`Restore ${f.path} from base`}
      class="hidden h-5 w-5 shrink-0 items-center justify-center rounded text-black-600 hover:text-red-600 group-hover:inline-flex dark:hover:text-red-400 disabled:opacity-50"
    >
      <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 8a6 6 0 0110.5-4M11 2v3H8M14 8a6 6 0 01-10.5 4M5 14v-3h3" stroke-linecap="round" stroke-linejoin="round"/></svg>
    </button>
  </div>
{/if}
