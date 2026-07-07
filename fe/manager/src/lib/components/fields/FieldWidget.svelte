<script lang="ts">
  /* Per-type config widget dispatcher. Reuses @wick-fe/common-ui primitives
     where they fit (TextInput for text/email/url, NumberInput, TextArea,
     Select) and the manager-local widgets for the rest (checkbox, date,
     datetime, color, secret).

     Every widget emits its new value through a single onChange. The parent
     ConfigsForm decides when to persist: immediate controls (select, checkbox,
     color, date, datetime) save on change, while free-text fields debounce —
     mirroring the legacy configs.templ attachAutoSave classification. */
  import { TextInput, NumberInput, TextArea, Select } from "@wick-fe/common-ui";
  import type { ConfigField } from "$lib/types.js";
  import { parseColOpts } from "./options.js";
  import CheckboxInput from "./CheckboxInput.svelte";
  import DateInput from "./DateInput.svelte";
  import ColorInput from "./ColorInput.svelte";
  import SecretInput from "./SecretInput.svelte";
  import HtmlField from "./HtmlField.svelte";

  type Props = {
    field: ConfigField;
    value: string;
    onChange: (v: string) => void;
    disabled?: boolean;
    /* Needed only by the server-rendered "html" widget, which calls a
       connector op via the manager /test path. Empty for other field types. */
    connectorKey?: string;
    connectorId?: string;
  };
  let { field, value, onChange, disabled = false, connectorKey = "", connectorId = "" }: Props = $props();

  let dropdownOptions = $derived([
    { label: "— select —", value: "" },
    ...parseColOpts(field.options),
  ]);
</script>

{#if field.is_secret}
  <SecretInput hasValue={field.has_value} {disabled} onChange={onChange} />
{:else if field.type === "textarea"}
  <TextArea {value} {disabled} onChange={onChange} rows={4} />
{:else if field.type === "dropdown"}
  <Select {value} {disabled} options={dropdownOptions} onChange={onChange} />
{:else if field.type === "html"}
  <HtmlField {connectorKey} {connectorId} op={field.options} {value} {disabled} onChange={onChange} />
{:else if field.type === "checkbox" || field.type === "bool" || field.type === "boolean"}
  <CheckboxInput {value} {disabled} onChange={onChange} />
{:else if field.type === "number"}
  <NumberInput value={Number(value) || 0} {disabled} onChange={(n) => onChange(String(n))} />
{:else if field.type === "email"}
  <TextInput type="email" {value} {disabled} placeholder="you@example.com" onChange={onChange} />
{:else if field.type === "url"}
  <TextInput type="url" {value} {disabled} placeholder="https://" onChange={onChange} />
{:else if field.type === "color"}
  <ColorInput {value} {disabled} onChange={onChange} />
{:else if field.type === "date"}
  <DateInput {value} {disabled} onChange={onChange} />
{:else if field.type === "datetime"}
  <DateInput {value} {disabled} withTime onChange={onChange} />
{:else}
  <TextInput {value} {disabled} onChange={onChange} />
{/if}
