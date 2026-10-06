/* Idle compact session rules: the text format of idle_compact_match as
   a structure the rule builder can edit. One line is one rule; its
   conditions are joined with & and must all hold. A condition is
   [!][project:|title:|id:]text, where text is a substring or /regex/.
   Mirrors ParseSessionPatterns in internal/agents/provider/idlecompact.go. */

export type RuleField = "" | "project" | "title" | "id";

export interface RuleCond {
  neg: boolean;
  field: RuleField;
  regex: boolean;
  value: string;
}

export interface Rule {
  conds: RuleCond[];
}

const FIELDS: RuleField[] = ["project", "title", "id"];

function stripField(s: string): { field: RuleField; rest: string } {
  const lower = s.toLowerCase();
  for (const f of FIELDS) {
    if (lower.startsWith(f + ":")) return { field: f, rest: s.slice(f.length + 1).trim() };
  }
  return { field: "", rest: s };
}

/* splitConds splits on & outside a /regular expression/. */
function splitConds(line: string): string[] {
  const out: string[] = [];
  let inRe = false;
  let start = 0;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (ch === "/") {
      if (inRe) inRe = false;
      else {
        const head = line.slice(start, i).trim().replace(/^!\s*/, "");
        if (stripField(head).rest.trim() === "") inRe = true;
      }
    } else if (ch === "&" && !inRe) {
      out.push(line.slice(start, i));
      start = i + 1;
    }
  }
  out.push(line.slice(start));
  return out;
}

export function parseCond(raw: string): RuleCond | null {
  let s = raw.trim();
  let neg = false;
  if (s.startsWith("!")) {
    neg = true;
    s = s.slice(1).trim();
  }
  const { field, rest } = stripField(s);
  s = rest.trim();
  if (!s) return null;
  if (s.length > 2 && s.startsWith("/") && s.endsWith("/")) {
    return { neg, field, regex: true, value: s.slice(1, -1) };
  }
  return { neg, field, regex: false, value: s };
}

export function parseRules(text: string): Rule[] {
  const rules: Rule[] = [];
  for (const line of text.split(/[\n,]/)) {
    const conds = splitConds(line).map(parseCond).filter((c): c is RuleCond => c !== null);
    if (conds.length) rules.push({ conds });
  }
  return rules;
}

export function formatCond(c: RuleCond): string {
  const v = c.value.trim();
  return `${c.neg ? "!" : ""}${c.field ? c.field + ":" : ""}${c.regex ? `/${v}/` : v}`;
}

/* formatRules drops empty conditions and empty rules. */
export function formatRules(rules: Rule[]): string {
  return rules
    .map((r) => r.conds.filter((c) => c.value.trim() !== "").map(formatCond).join(" & "))
    .filter((l) => l !== "")
    .join("\n");
}

export function emptyCond(): RuleCond {
  return { neg: false, field: "title", regex: false, value: "" };
}
