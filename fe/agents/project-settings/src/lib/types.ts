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
  system_addon: string;
  chat_count: number;
  created_at: string;
  preset_list: string[];
  provider_list: ProviderListItem[];
  pinned: PinnedSession[];
  meta_json: string;
  action: string;
}

export interface UpdateProjectRequest {
  name: string;
  icon: string;
  description: string;
  folder_mode: string;
  custom_path: string;
  preset: string;
  provider: string;
  system_addon: string;
}
