export interface PinnedSession {
  id: string;
  label: string;
}

export interface ProviderListItem {
  type: string;
  name: string;
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
