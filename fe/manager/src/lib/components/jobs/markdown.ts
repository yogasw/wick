/* renderMarkdownSafe converts a safe subset of markdown to HTML, ported
   verbatim (behaviour-for-behaviour) from the legacy jobs.js. All HTML
   entities are escaped first, so no raw user HTML can pass through — the
   output is safe to inject as innerHTML. Only used for job run output. */
export function renderMarkdownSafe(md: string): string {
  let s = md
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");

  s = s.replace(/```(\w*)\n([\s\S]*?)```/g, (_m, _lang, code) =>
    `<pre class="rounded-lg bg-white-200 dark:bg-navy-800 p-3 text-xs font-mono overflow-x-auto"><code>${code.trim()}</code></pre>`,
  );
  s = s.replace(/`([^`]+)`/g, (_m, code) =>
    `<code class="rounded bg-white-200 dark:bg-navy-800 px-1.5 py-0.5 text-xs font-mono">${code}</code>`,
  );
  s = s.replace(/^### (.+)$/gm, '<h3 class="text-sm font-semibold mt-3 mb-1">$1</h3>');
  s = s.replace(/^## (.+)$/gm, '<h2 class="text-base font-semibold mt-4 mb-1">$1</h2>');
  s = s.replace(/^# (.+)$/gm, '<h1 class="text-lg font-semibold mt-4 mb-2">$1</h1>');
  s = s.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/\*(.+?)\*/g, "<em>$1</em>");
  s = s.replace(/^[*-] (.+)$/gm, '<li class="ml-4 list-disc">$1</li>');
  s = s.replace(/^\d+\. (.+)$/gm, '<li class="ml-4 list-decimal">$1</li>');
  s = s.replace(/^---$/gm, '<hr class="my-3 border-white-300 dark:border-navy-600"/>');
  s = s.replace(/\n\n/g, '</p><p class="mt-2">');
  s = s.replace(/\n/g, "<br/>");

  return `<div class="text-sm">${s}</div>`;
}
