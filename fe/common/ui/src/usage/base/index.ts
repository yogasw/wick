/* Usage base module: the provider-agnostic types, wire parser and the one
 * client-side usage cache. No Svelte components and nothing app-specific,
 * so TS-only code (API normalisers, other apps, a future provider's UI)
 * imports "@wick-fe/common-ui/usage" without pulling in the component
 * library. The components built on it live one level up (../usage/*.svelte)
 * and the per-app wiring stays in each app. */
export {
  parseSavedResets,
  soonestExpiry,
  expiresSoon,
  showSavedResetsChip,
  savedResetsTooltip,
  usableNowText,
  inCooldown,
  ringTone,
  fmtResetDate,
  fmtResetDateTime,
  SAVED_RESET_SOON_DAYS,
} from "./savedResets.js";
export type { SavedResets, SavedResetItem, WireSavedResets, RingTone } from "./savedResets.js";
export {
  usageStore,
  loadUsage,
  seedUsage,
  peekUsage,
  glanceFromWire,
  resetUsageStore,
  USAGE_TTL_MS,
} from "./usageStore.js";
export type { UsageGlance, GlanceWindow, WireGlance } from "./usageStore.js";
