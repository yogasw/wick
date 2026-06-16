export function extOf(p: string): string {
  const i = p.lastIndexOf(".");
  return i === -1 ? "" : p.slice(i + 1).toLowerCase();
}

const MODE_BY_EXT: Record<string, string> = {
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  ts: "typescript",
  tsx: "tsx",
  jsx: "jsx",
  go: "golang",
  py: "python",
  rb: "ruby",
  rs: "rust",
  java: "java",
  c: "c_cpp",
  cpp: "c_cpp",
  h: "c_cpp",
  cs: "csharp",
  php: "php",
  swift: "swift",
  kt: "kotlin",
  sh: "sh",
  bash: "sh",
  zsh: "sh",
  ps1: "powershell",
  json: "json",
  yaml: "yaml",
  yml: "yaml",
  toml: "toml",
  xml: "xml",
  html: "html",
  htm: "html",
  css: "css",
  scss: "scss",
  sass: "sass",
  md: "markdown",
  markdown: "markdown",
  sql: "sql",
  dockerfile: "dockerfile",
};

export function aceModeFor(path: string): string {
  const e = extOf(path);
  return `ace/mode/${MODE_BY_EXT[e] ?? "text"}`;
}
