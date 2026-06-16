// SCM API — git source control for a session's working directory.
// All endpoints live under the agents tool mount and are session-scoped.

import { apiGet, apiPost } from "@wick-fe/common-api";

const BASE = "/tools/agents";

export type RepoSummary = {
  rel: string;
  name: string;
  branch: string;
  changed: number;
  ahead: number;
  behind: number;
};

// Full session-wide snapshot: returned by GET /git/repos AND pushed over
// SSE (git_status). statuses is keyed by RepoSummary.rel.
export type GitStatusSnapshot = {
  repos: RepoSummary[];
  statuses: Record<string, StatusResult>;
  total_changed: number;
};

export type FileChange = {
  path: string;
  orig_path?: string;
  index: string;
  work_tree: string;
  staged: boolean;
  unstaged: boolean;
  untracked: boolean;
};

export type BranchInfo = {
  name: string;
  upstream?: string;
  ahead: number;
  behind: number;
  detached: boolean;
};

export type StatusResult = {
  branch: BranchInfo;
  changes: FileChange[];
};

export type BranchList = {
  current: string;
  branches: string[];
  remotes: string[];
};

export type LogEntry = {
  sha: string;
  subject: string;
  author: string;
  rel_date: string;
  iso_date: string;
};

export type CommitFile = {
  path: string;
  status: string;
};

export type CommitDetail = {
  sha: string;
  subject: string;
  author: string;
  iso_date: string;
  files: CommitFile[];
};

const s = (id: string) => `${BASE}/api/sessions/${encodeURIComponent(id)}/git`;
const q = (v: string) => encodeURIComponent(v);

export const getRepos = (id: string) => apiGet<GitStatusSnapshot>(`${s(id)}/repos`);

export const getStatus = (id: string, repo: string) =>
  apiGet<StatusResult>(`${s(id)}/status?repo=${q(repo)}`);

export const getDiff = (id: string, repo: string, path: string, staged: boolean) =>
  apiGet<{ diff: string }>(
    `${s(id)}/diff?repo=${q(repo)}&path=${q(path)}&staged=${staged ? "1" : "0"}`,
  );

export const getFile = (id: string, repo: string, path: string) =>
  apiGet<{ content: string; path: string }>(
    `${s(id)}/file?repo=${q(repo)}&path=${q(path)}`,
  );

export const getBranches = (id: string, repo: string) =>
  apiGet<BranchList>(`${s(id)}/branches?repo=${q(repo)}`);

export const stage = (id: string, repo: string, paths: string[]) =>
  apiPost(`${s(id)}/stage`, { repo, paths });

export const unstage = (id: string, repo: string, paths: string[]) =>
  apiPost(`${s(id)}/unstage`, { repo, paths });

// Discard is destructive (git restore / clean). untracked lists which of
// paths are untracked so the server picks clean vs restore.
export const discard = (id: string, repo: string, paths: string[], untracked: string[]) =>
  apiPost(`${s(id)}/discard`, { repo, paths, untracked });

export const commit = (id: string, repo: string, message: string) =>
  apiPost<{ sha: string }>(`${s(id)}/commit`, { repo, message });

export const switchBranch = (id: string, repo: string, branch: string) =>
  apiPost(`${s(id)}/branch/switch`, { repo, branch });

export const createBranch = (id: string, repo: string, branch: string, checkout: boolean) =>
  apiPost(`${s(id)}/branch/create`, { repo, branch, checkout });

export const push = (id: string, repo: string) =>
  apiPost<{ output: string }>(`${s(id)}/push`, { repo });

export const pull = (id: string, repo: string) =>
  apiPost<{ output: string }>(`${s(id)}/pull`, { repo });

export const saveFile = (id: string, repo: string, path: string, content: string) =>
  apiPost(`${s(id)}/file`, { repo, path, content });

// Raw content at a ref (default HEAD) — the "original" side for Monaco's
// diff editor. Empty content means the file didn't exist at that ref.
export const getBlob = (id: string, repo: string, path: string, ref = "HEAD") =>
  apiGet<{ content: string; path: string; ref: string }>(
    `${s(id)}/blob?repo=${q(repo)}&path=${q(path)}&ref=${q(ref)}`,
  );

// Two raw sides for a working-tree change, picked git-correctly by the
// server (staged → HEAD↔index, unstaged → index↔working).
export const getCompare = (
  id: string,
  repo: string,
  path: string,
  staged: boolean,
  untracked: boolean,
) =>
  apiGet<{ original: string; modified: string; path: string }>(
    `${s(id)}/compare?repo=${q(repo)}&path=${q(path)}&staged=${staged ? "1" : "0"}&untracked=${untracked ? "1" : "0"}`,
  );

export const getLog = (id: string, repo: string, limit = 50) =>
  apiGet<{ commits: LogEntry[] }>(`${s(id)}/log?repo=${q(repo)}&limit=${limit}`);

export const getCommit = (id: string, repo: string, sha: string) =>
  apiGet<CommitDetail>(`${s(id)}/commit?repo=${q(repo)}&sha=${q(sha)}`);

export const getCommitDiff = (id: string, repo: string, sha: string, path: string) =>
  apiGet<{ original: string; modified: string; path: string }>(
    `${s(id)}/commit-diff?repo=${q(repo)}&sha=${q(sha)}&path=${q(path)}`,
  );
