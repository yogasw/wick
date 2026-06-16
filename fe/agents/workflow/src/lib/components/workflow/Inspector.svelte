<script lang="ts">
  import { selectedNode, updateNode } from "$lib/stores/editor";
  import BaseInspectorPanel from "./nodes/BaseInspectorPanel.svelte";
  import { Select } from "@wick-fe/common-ui";
  import type { Node } from "$lib/types/workflow";

  function patch(field: keyof Node, value: unknown) {
    if (!$selectedNode) return;
    updateNode($selectedNode.id, { [field]: value } as Partial<Node>);
  }

  // Per-type form fields — kept inline because most are 3-6 inputs.
  // Pulled out to dedicated `<Type>Inspector.svelte` files once a type
  // grows non-trivial (notably classify cases editor + http headers
  // table — those become their own components in the next pass).
</script>

{#if $selectedNode}
  {@const n = $selectedNode}
  <BaseInspectorPanel node={n} onSave={() => {}} onCancel={() => {}}>
    {#snippet body()}
      <label class="flex flex-col gap-1">
        <span>Label</span>
        <input class="rounded border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1"
               value={n.label ?? ""}
               oninput={(e) => patch("label", (e.target as HTMLInputElement).value)} />
      </label>

      {#if n.type === "classify" || n.type === "agent"}
        <label class="flex flex-col gap-1">
          <span>Provider</span>
          <input class="rounded border px-2 py-1" value={n.provider ?? ""} oninput={(e) => patch("provider", (e.target as HTMLInputElement).value)} />
        </label>
        <label class="flex flex-col gap-1">
          <span>Preset</span>
          <input class="rounded border px-2 py-1" value={n.preset ?? ""} oninput={(e) => patch("preset", (e.target as HTMLInputElement).value)} />
        </label>
        <label class="flex flex-col gap-1">
          <span>Prompt</span>
          <textarea class="rounded border px-2 py-1 min-h-[100px] font-mono text-[11px]"
                    value={n.prompt ?? ""}
                    oninput={(e) => patch("prompt", (e.target as HTMLTextAreaElement).value)}></textarea>
        </label>
      {/if}

      {#if n.type === "classify"}
        <label class="flex flex-col gap-1">
          <span>Output cases (comma-separated)</span>
          <input class="rounded border px-2 py-1"
                 value={(n.output_cases ?? []).join(", ")}
                 oninput={(e) => patch("output_cases", (e.target as HTMLInputElement).value.split(",").map((s) => s.trim()).filter(Boolean))} />
        </label>
      {/if}

      {#if n.type === "branch"}
        <label class="flex flex-col gap-1">
          <span>Expression</span>
          <input class="rounded border px-2 py-1 font-mono" value={n.expr ?? ""} oninput={(e) => patch("expr", (e.target as HTMLInputElement).value)} />
        </label>
      {/if}

      {#if n.type === "http"}
        <label class="flex flex-col gap-1">
          <span>Method</span>
          <Select value={n.method ?? "GET"} options={["GET","POST","PUT","PATCH","DELETE"]} size="sm" onChange={(v) => patch("method", v)} />
        </label>
        <label class="flex flex-col gap-1">
          <span>URL</span>
          <input class="rounded border px-2 py-1 font-mono" value={n.url ?? ""} oninput={(e) => patch("url", (e.target as HTMLInputElement).value)} />
        </label>
        <label class="flex flex-col gap-1">
          <span>Body</span>
          <textarea class="rounded border px-2 py-1 font-mono min-h-[80px]" value={n.body ?? ""} oninput={(e) => patch("body", (e.target as HTMLTextAreaElement).value)}></textarea>
        </label>
      {/if}

      {#if n.type === "db_query"}
        <label class="flex flex-col gap-1">
          <span>Database</span>
          <input class="rounded border px-2 py-1" value={n.database ?? ""} oninput={(e) => patch("database", (e.target as HTMLInputElement).value)} />
        </label>
        <label class="flex flex-col gap-1">
          <span>SQL</span>
          <textarea class="rounded border px-2 py-1 font-mono min-h-[120px]" value={n.sql ?? ""} oninput={(e) => patch("sql", (e.target as HTMLTextAreaElement).value)}></textarea>
        </label>
      {/if}

      {#if n.type === "shell"}
        <label class="flex flex-col gap-1">
          <span>Command (space-separated)</span>
          <input class="rounded border px-2 py-1 font-mono" value={(n.command ?? []).join(" ")} oninput={(e) => patch("command", (e.target as HTMLInputElement).value.split(/\s+/).filter(Boolean))} />
        </label>
        <label class="flex flex-col gap-1">
          <span>Cwd</span>
          <input class="rounded border px-2 py-1 font-mono" value={n.cwd ?? ""} oninput={(e) => patch("cwd", (e.target as HTMLInputElement).value)} />
        </label>
      {/if}

      {#if n.type === "transform"}
        <label class="flex flex-col gap-1">
          <span>Engine</span>
          <input class="rounded border px-2 py-1" value={n.engine ?? "template"} oninput={(e) => patch("engine", (e.target as HTMLInputElement).value)} />
        </label>
        <label class="flex flex-col gap-1">
          <span>Expression</span>
          <textarea class="rounded border px-2 py-1 font-mono min-h-[80px]" value={n.expression ?? ""} oninput={(e) => patch("expression", (e.target as HTMLTextAreaElement).value)}></textarea>
        </label>
      {/if}

      {#if n.type === "go_script" || n.type === "python"}
        <label class="flex flex-col gap-1">
          <span>Code</span>
          <textarea class="rounded border px-2 py-1 font-mono min-h-[180px]" value={n.code ?? ""} oninput={(e) => patch("code", (e.target as HTMLTextAreaElement).value)}></textarea>
        </label>
      {/if}

      {#if n.type === "connector"}
        <label class="flex flex-col gap-1">
          <span>Module</span>
          <input class="rounded border px-2 py-1" value={n.module ?? ""} oninput={(e) => patch("module", (e.target as HTMLInputElement).value)} />
        </label>
        <label class="flex flex-col gap-1">
          <span>Op</span>
          <input class="rounded border px-2 py-1" value={n.op ?? ""} oninput={(e) => patch("op", (e.target as HTMLInputElement).value)} />
        </label>
      {/if}

      {#if n.type === "channel"}
        <label class="flex flex-col gap-1">
          <span>Channel</span>
          <input class="rounded border px-2 py-1" value={n.channel ?? ""} oninput={(e) => patch("channel", (e.target as HTMLInputElement).value)} />
        </label>
        <label class="flex flex-col gap-1">
          <span>Op</span>
          <input class="rounded border px-2 py-1" value={n.op ?? ""} oninput={(e) => patch("op", (e.target as HTMLInputElement).value)} />
        </label>
      {/if}

      {#if n.type === "session_init"}
        <label class="flex flex-col gap-1">
          <span>Preset</span>
          <input class="rounded border px-2 py-1" value={n.preset ?? ""} oninput={(e) => patch("preset", (e.target as HTMLInputElement).value)} />
        </label>
        <label class="flex flex-col gap-1">
          <span>Session ID (optional)</span>
          <input class="rounded border px-2 py-1 font-mono" value={n.session_id ?? ""} oninput={(e) => patch("session_id", (e.target as HTMLInputElement).value)} />
        </label>
      {/if}
    {/snippet}

    {#snippet advanced()}
      <label class="flex flex-col gap-1">
        <span>Timeout (sec)</span>
        <input type="number" class="rounded border px-2 py-1" value={n.timeout_sec ?? 0} oninput={(e) => patch("timeout_sec", Number((e.target as HTMLInputElement).value) || 0)} />
      </label>
      <label class="flex flex-col gap-1">
        <span>On failure</span>
        <Select value={n.on_failure ?? "stop"} options={["stop","continue","fallback"]} size="sm" onChange={(v) => patch("on_failure", v)} />
      </label>
      <label class="flex flex-col gap-1">
        <span>Fallback node</span>
        <input class="rounded border px-2 py-1 font-mono" value={n.fallback ?? ""} oninput={(e) => patch("fallback", (e.target as HTMLInputElement).value)} />
      </label>
    {/snippet}
  </BaseInspectorPanel>
{/if}
<!-- Inspector hides entirely when no node selected — matches the legacy
     editor where the canvas owns the full viewport until the user
     clicks a node. -->

