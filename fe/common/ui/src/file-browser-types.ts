/* The shapes the file browser speaks. They used to live in the conversation
   shell's types; the browser is shared now, so they are too — the SCM panel
   lists the selected repo with the same component and the same rows. */

/** One entry in a session's working directory, path relative to whatever
    root the caller is showing (the session cwd, or a repo inside it). */
export type SessionFileEntry = {
  path: string;
  name: string;
  size: number;
  isDir: boolean;
  /** Unix ms. 0 when the server did not say. */
  mtime: number;
};

/** A file's bytes, as the read endpoint returns them. content is absent for
    a binary or oversized file — the editor cannot show it and the caller
    offers a download instead. */
export type FileContent = {
  path: string;
  size: number;
  binary: boolean;
  content?: string;
  tooBig?: boolean;
  mtime?: number;
};
