export interface PinnedSession {
  id: string;
  label: string;
}

/* Provider shapes live in common-api: the sub-agent role editor renders the
   same list, and a second declaration here would be a second thing to keep
   in step with the server. Imported for local use AND re-exported, so the
   modules already importing them from here keep working. */
import type { ProviderListItem } from "@wick-fe/common-api";
export type { ProviderListItem, ProviderModelItem } from "@wick-fe/common-api";

/** Tri-state for one configurable widget CSP directive. */
export type WidgetMode = "block" | "list" | "all";

/** The preset that decides the whole posture: secure seals everything,
    unsecure opens everything, and only custom reads the fields below. */
export type WidgetPreset = "secure" | "unsecure" | "custom";

/** A project's stored widget CSP override. `override: false` means the
    project inherits the global policy and every other field is ignored;
    `mode` then overrides the per-directive fields unless it is "custom". */
export interface WidgetPolicy {
  override?: boolean;
  mode?: string;
  frame_src?: string;
  img_src?: string;
  media_src?: string;
  connect_src?: string;
  /** External scripts only — the widget's own inline scripts always run. */
  script_src?: string;
  allow_popups?: boolean;
  /** Gives an opened tab a real origin instead of the sandboxed "null" one.
      Only meaningful alongside allow_popups. */
  allow_popup_escape?: boolean;
  allowlist?: string[];
}

export interface ProjectSettingsData {
  id: string;
  name: string;
  icon: string;
  description: string;
  custom_path: string;
  managed: boolean;
  is_protected: boolean;
  is_new: boolean;
  default_preset: string;
  default_provider: string;
  /** Model pinned on default_provider, in that instance's own id space
      (for wick, "<entryID>@<vendorModelID>" — down to a live-set leaf).
      Only meaningful alongside default_provider. */
  default_model: string;
  system_addon: string;
  chat_count: number;
  created_at: string;
  preset_list: string[];
  provider_list: ProviderListItem[];
  pinned: PinnedSession[];
  meta_json: string;
  action: string;
  /** This project's own widget CSP override (not the resolved policy). */
  widget?: WidgetPolicy;
  /** The global policy this project falls back to. Shown read-only so the
      operator can see what the project's hosts are appended TO. */
  widget_inherited?: WidgetPolicy;
}

export interface UpdateProjectRequest {
  name: string;
  icon: string;
  description: string;
  folder_mode: string;
  custom_path: string;
  preset: string;
  provider: string;
  /** Sent with provider, never alone: a model id resolves against one
      instance's registry, so the server drops it when provider is empty. */
  model: string;
  system_addon: string;
  /** Widget CSP override. The allowlist travels as the raw textarea text so
      the server can name the offending line back to the operator. Omitted
      entirely leaves the stored override untouched. */
  widget?: {
    override: boolean;
    mode: string;
    frame_src: string;
    img_src: string;
    media_src: string;
    connect_src: string;
    script_src: string;
    allow_popups: boolean;
    allow_popup_escape: boolean;
    allowlist: string;
  };
}
