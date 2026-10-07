export { default as ConfirmDialog } from "./ConfirmDialog.svelte";
export { default as DeleteProjectDialog } from "./DeleteProjectDialog.svelte";
export type { DeleteProjectPreview } from "./delete-project.js";
export { canConfirmDelete, deleteProjectBody, needsTypedName } from "./delete-project.js";
export { default as ToastHost } from "./ToastHost.svelte";
export { default as Select } from "./Select.svelte";
export type { SelectOption } from "./select-types.js";
export { default as KvList } from "./KvList.svelte";
export { default as Button } from "./Button.svelte";
export { default as AIGenerateButton } from "./AIGenerateButton.svelte";
export type { AIGenInput, AIGenJob, AIGenState, AIGenStatus, AIGenPhase, AIGenAPI } from "./aigen.js";
export { AIGenRun, AIGEN_ENDPOINT, submitAIGen, getAIGen, cancelAIGen, idleAIGenState } from "./aigen.js";
export { default as ProgressBar } from "./ProgressBar.svelte";
export { default as Toggle } from "./Toggle.svelte";
export { default as TextInput } from "./TextInput.svelte";
export { default as NumberInput } from "./NumberInput.svelte";
export { default as TextArea } from "./TextArea.svelte";
export { default as LabeledInput } from "./LabeledInput.svelte";
export { default as Modal } from "./Modal.svelte";
export { default as ScheduleEditModal } from "./ScheduleEditModal.svelte";
export type {
  EditableSchedule,
  SchedulePatchInput,
  ScheduleProjectOption,
  ScheduleSessionMode,
} from "./schedule-edit-types.js";
export {
  formatScheduleTime,
  isLegalSessionID,
  isProjectScopedSchedule,
  renderSessionTemplate,
  scheduleCadence,
} from "./schedule-edit-types.js";
export { default as Breadcrumb } from "./Breadcrumb.svelte";
export type { BreadcrumbItem } from "./Breadcrumb.svelte";
export { default as KebabMenu } from "./KebabMenu.svelte";
export { copyText } from "./clipboard.js";
export { default as CodeEditor } from "./CodeEditor.svelte";
export { aceModeFor, aceModeForLanguage, extOf } from "./aceMode.js";
export { default as Composer } from "./Composer.svelte";
export { default as ProviderPicker } from "./ProviderPicker.svelte";
export { default as ProviderIcon } from "./ProviderIcon.svelte";
export { providerBrand } from "./provider-brand.js";
export type { ProviderBrand } from "./provider-brand.js";
export { buildProviderOptions } from "./provider-options.js";
export type { ComposerSelectOption, ComposerModelOption } from "./composer-types.js";
export { default as ImageEditor } from "./ImageEditor.svelte";
export type { ComposerCommand, ComposerSelect, ComposerMentionAgent } from "./composer-types.js";
export { agentMentionRows, fileMentionRows, MENTION_GROUPS } from "./mention-menu.js";
export { default as CapabilityChips } from "./CapabilityChips.svelte";
export { default as CapabilityModal } from "./CapabilityModal.svelte";
export { default as AgentProfileEditor } from "./AgentProfileEditor.svelte";
export { default as AgentProfileRow } from "./AgentProfileRow.svelte";
export { default as AgentModelQuickChange } from "./AgentModelQuickChange.svelte";
export { default as ConfigForm } from "./ConfigForm.svelte";
export type { ConfigField } from "./config-types.js";
export { dropdownOptions, isVisible, isToggle } from "./config-types.js";
export type { ModelCaps, CapabilityDisplayMode } from "./capability-types.js";
export { CAP_DESCRIPTORS, capDescriptor, fmtTokens, hasAnyCaps } from "./capability-types.js";
export { default as UsageReport } from "./UsageReport.svelte";
export {
  compactTokens,
  formatCost,
  exact,
  sinceText,
  fetchUsageReport,
  fetchProviderUsage,
  EMPTY_TOTALS,
} from "./usageReport.js";
export type {
  UsageReport as UsageReportData,
  UsageSlice,
  UsageTotals,
  SessionUse,
  WindowOption,
  ProviderUsageDetail,
} from "./usageReport.js";
export {
  contextMeter,
  contextTone,
  contextBarClass,
  contextTextClass,
  budgetText,
  tokenBudgetText,
} from "./context-meter.js";
export type {
  ContextReading,
  ContextMeterKind,
  ContextMeterView,
  ContextTone,
} from "./context-meter.js";

// File browser — the session Files rail and the Source panel's Files tab
// are the same component pointed at different roots.
export { default as FileBrowser } from "./FileBrowser.svelte";
export { default as FileBrowserNode } from "./FileBrowserNode.svelte";
export type { SessionFileEntry, FileContent } from "./file-browser-types.js";
export type { FileTreeNode, SortKey } from "./file-browser-tree.js";
export {
  buildFileTree,
  compareNodes,
  filterFileTree,
  sortTree,
  ext,
} from "./file-browser-tree.js";
export { rankPathHits, scorePath, subsequence, withAncestorDirs } from "./file-search.js";
export type { PathHit } from "./file-search.js";
export { formatSize, formatRelTime } from "./file-meta.js";
export { matchModelFilter, MODEL_FILTER_HELP } from "./modelFilter.js";
export { encodePath, decodePath, encodePin, decodePin } from "./model-path.js";
export { withModelListMeta, optionModelsWithMeta, modelListMeta, describeModelListMeta } from "./model-list-meta.js";
export type { ModelListMeta } from "./model-list-meta.js";
// Trace rendering — one tool call / result, dispatched on its Display kind.
// See src/trace/README.md for adding a kind.
export { default as TraceBody } from "./trace/TraceBody.svelte";
export { default as ScrollBox } from "./trace/ScrollBox.svelte";
export { default as JsonTree } from "./JsonTree.svelte";
export { registerTraceRenderer, getTraceRenderer } from "./trace/registry.js";
export type { TraceRenderer, TraceRendererProps } from "./trace/registry.js";
export { classifyCall, classifyResult, registerTraceDetector, toolFamily, langForPath } from "./trace/classify.js";
export { setTraceHighlighter } from "./trace/highlight.js";
export { humanBytes } from "./trace/format.js";
export { parseSkill, skillDisplay, skillScope, isSkillPath } from "./trace/skill.js";
export type { SkillDoc, SkillScope } from "./trace/skill.js";
export type { TraceDisplay, TraceContext, TraceMedia, TraceFileStat } from "./trace/types.js";
export { traceFileState, type TraceFileState } from "./trace/fileState.js";
export { isBinaryKind } from "./trace/types.js";
export { pushLayer, layer, topLayerNode, layerDepth } from "./layers.js";
export type { LayerOptions } from "./layers.js";
export { clock24 } from "./time24.js";
