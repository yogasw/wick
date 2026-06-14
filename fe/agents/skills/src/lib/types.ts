export interface SkillListItem {
  name: string;
  is_dir: boolean;
  in_dirs: string[];
  missing_dirs: string[];
}

export interface SkillListResponse {
  dirs: string[];
  skills: SkillListItem[];
}

export interface SkillDetailResponse {
  name: string;
  is_dir: boolean;
  content?: string;
  source_path?: string;
  in_dirs: string[];
  entries?: SkillListItem[];
  missing_dirs?: string[];
}

export interface SkillFileDetailResponse {
  name: string;
  is_dir: boolean;
  content?: string;
  source_path?: string;
  in_dirs: string[];
  entries?: SkillListItem[];
}

export interface SkillProviderEntryResponse {
  provider: string;
  path: string;
  is_dir: boolean;
  content?: string;
  source_path?: string;
  entries?: SkillListItem[];
  all_providers: string[];
  has_file?: Record<string, boolean>;
}
