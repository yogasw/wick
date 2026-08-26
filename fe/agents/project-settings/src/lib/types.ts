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
  /** Ticket-mode configuration ("Ticket system" section). */
  ticket?: TicketConfig;
}

/** One custom field in the project's ticket schema. */
export interface TicketField {
  key: string;
  label: string;
  type: "text" | "select";
  options?: string[];
  required?: boolean;
  /** Show this field's value on the board card. Off by default — the full
      set is always on the ticket's own page. */
  show_on_card?: boolean;
}

/** One custom action button on every ticket's page. Clicking it POSTs the
    ticket to `url` as a ticket.action event (e.g. "Sync to Notion"). */
export interface TicketButton {
  id?: string; // minted server-side on first save
  label: string;
  url: string;
}

/** One column on the project's board. `key` is what tickets store and what
    MCP accepts; `label` is display only. Exactly one status is `terminal` —
    the stage auto-resolve moves finished work to. */
export interface TicketStatus {
  key: string;
  label?: string;
  terminal?: boolean;
}

/** One rule deciding when a new session is given a ticket with nobody
    asking. Rules are tried in order and the FIRST match wins, so a
    disabled narrow rule above a broad one carves an exception out of it —
    that is how "everything from Slack except DMs" is written. */
export interface AutoCreateRule {
  /** "ui" | "slack" | "telegram" | "rest" | "*" */
  origin: string;
  /** "" (any) | "dm" | "channel" | "thread" — channel origins only. */
  channel_kind?: string;
  /** "" | "contains:<text>" | "regex:<expr>", tested against the first message. */
  match?: string;
  /** Title template; "{message}" and "{origin}" are substituted. */
  title?: string;
  enabled: boolean;
}

/** A project's ticket-mode configuration. Zero value = feature off. */
export interface TicketConfig {
  enabled?: boolean;
  fields?: TicketField[];
  followup_after_sec?: number;
  followup_prompt?: string;
  auto_resolve_after_sec?: number;
  auto_create?: AutoCreateRule[];
  /** Board columns, in order. Empty means the built-in set. */
  statuses?: TicketStatus[];
  integrations?: TicketIntegrations;
}

/** Outbound webhooks + the token-authed REST surface. */
export interface TicketIntegrations {
  /** Lets a Personal Access Token call this project's ticket endpoints. */
  api_enabled?: boolean;
  webhooks?: TicketWebhook[];
  /** Custom action buttons rendered on every ticket's page. */
  buttons?: TicketButton[];
}

/** One outbound endpoint. */
export interface TicketWebhook {
  /** Stable across edits, so the delivery log survives a URL change. */
  id: string;
  name?: string;
  url: string;
  /** Never the real value on read: `__stored__` means "signed, key hidden",
      empty means unsigned. Sending `__stored__` back keeps the stored key. */
  secret?: string;
  /** Empty means every event. */
  events?: string[];
  headers?: Record<string, string>;
  enabled: boolean;
}

/** One recorded delivery attempt, for the settings page. */
export interface TicketDelivery {
  webhook_id: string;
  event: string;
  at: string;
  status?: number;
  error?: string;
  attempts: number;
  ok: boolean;
}

/** Sentinel standing in for a stored webhook secret. */
export const SECRET_REDACTED = "__stored__";

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
