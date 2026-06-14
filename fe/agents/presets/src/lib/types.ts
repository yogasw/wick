export interface PresetItem {
  name: string;
  is_default?: boolean;
}

export interface PresetListResponse {
  presets: PresetItem[];
}

export interface PresetDetailResponse {
  name: string;
  body: string;
}
