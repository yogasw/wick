<script lang="ts">
  // SchemaForm — renders an entity.Config[] row list against a value
  // map, picking the right primitive per row.Type:
  //   dropdown → Field kind="select"
  //   textarea → Field kind="textarea"
  //   checkbox → Field kind="checkbox"
  //   number   → Field kind="number"
  //   picker   → PickerField (typeahead via /workflows/api/lookup)
  //   *        → Field kind="text"
  //
  // Honours visible_when (`field:val` or `field:a|b|c`) so dependent
  // rows surface only while their gate matches. Honours `hidden`
  // (`wick:"hidden"`) by skipping the row entirely.
  //
  // Used by both the trigger match form and the node channel /
  // connector args inspector — same schema, same primitives, no
  // duplicated render logic.
  import Field from "./Field.svelte";
  import PickerField from "./PickerField.svelte";
  import type { CatalogConfigField } from "$lib/api/workflow";

  type Props = {
    schema: CatalogConfigField[];
    values: Record<string, unknown>;
    onChange: (key: string, value: unknown) => void;
    onClear?: (key: string) => void;
  };

  let { schema, values, onChange, onClear }: Props = $props();

  function isVisible(f: CatalogConfigField): boolean {
    if (f.hidden) return false;
    if (!f.visible_when) return true;
    const idx = f.visible_when.indexOf(":");
    if (idx < 0) return true;
    const key = f.visible_when.slice(0, idx).trim();
    const expected = f.visible_when.slice(idx + 1).trim();
    const allowed = expected.split("|").map((s) => s.trim());
    const current = values[key];
    const currentStr = current === undefined || current === null ? "" : String(current);
    return allowed.includes(currentStr);
  }

  function dropdownOptions(f: CatalogConfigField): string[] {
    return (f.Options ?? "").split("|").filter(Boolean);
  }

  function kindFor(t: string | undefined): "text" | "textarea" | "number" | "select" | "checkbox" {
    switch (t) {
      case "dropdown":
        return "select";
      case "textarea":
        return "textarea";
      case "checkbox":
      case "bool":
      case "boolean":
        return "checkbox";
      case "number":
        return "number";
      default:
        return "text";
    }
  }
</script>

<div class="space-y-2">
  {#each schema.filter(isVisible) as f (f.Key)}
    {@const v = values[f.Key]}
    <div>
      {#if f.Type === "picker"}
        <PickerField
          label={f.Key}
          source={f.Options ?? ""}
          value={typeof v === "string" ? v : ""}
          onChange={(nv) => onChange(f.Key, nv)}
          helper={f.Description}
          required={f.Required}
          placeholder={`Search ${f.Options ?? "items"}…`}
        />
      {:else}
        <Field
          kind={kindFor(f.Type)}
          label={f.Key}
          value={v ?? (f.Type === "checkbox" || f.Type === "bool" ? false : "")}
          onChange={(nv) => onChange(f.Key, nv)}
          options={dropdownOptions(f)}
          helper={f.Description}
          required={f.Required}
          placeholder={f.Value}
        />
        {#if onClear && v !== undefined && v !== "" && v !== false && v !== null && f.Type !== "checkbox" && f.Type !== "bool"}
          <button
            type="button"
            class="text-[10px] text-rose-500 mt-0.5"
            onclick={() => onClear(f.Key)}
          >clear</button>
        {/if}
      {/if}
    </div>
  {/each}
</div>
