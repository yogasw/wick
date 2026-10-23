/* What the project delete dialog says and when it lets the user confirm.
   Kept out of the component so the wording and the type-the-name gate are
   tested without rendering anything. */

/* GET <base>/projects/<id>/delete-preview */
export type DeleteProjectPreview = {
  id: string;
  name: string;
  chats: number;
  custom_path?: string;
  protected: boolean;
};

export function deleteProjectTitle(p: { name: string }): string {
  return `Delete project ${p.name}?`;
}

export function deleteProjectBody(p: DeleteProjectPreview): string {
  const chats = `${p.chats} chat${p.chats === 1 ? "" : "s"}`;
  if (p.custom_path) {
    return (
      `This deletes ${chats} and the agents' memory for this project. ` +
      `Its folder ${p.custom_path} is yours and stays on disk. This cannot be undone.`
    );
  }
  return `This deletes ${chats}, its files folder and the agents' memory for this project. This cannot be undone.`;
}

/* A delete takes every chat and ticket with it and cannot be undone, so it
   is never one click: the name typed out, the consequence acknowledged, and
   two small sums answered. The sums make the step deliberate — nobody
   solves them by reflex on a dialog they opened by mistake. */
export const DELETE_CONSENT =
  "I understand this permanently deletes this project, all its chats and all its tickets. This cannot be undone.";

export type Sum = { a: number; b: number };

/* Two "A × B" questions, A and B in 2..9, never the same pair twice (in
   either order). rng is injectable so tests are deterministic. */
export function makeSums(rng: () => number = Math.random): [Sum, Sum] {
  const n = () => 2 + Math.floor(rng() * 8);
  const first = { a: n(), b: n() };
  for (let i = 0; i < 50; i++) {
    const second = { a: n(), b: n() };
    const same =
      (second.a === first.a && second.b === first.b) ||
      (second.a === first.b && second.b === first.a);
    if (!same) return [first, second];
  }
  // An rng stuck on one value: step off it rather than loop forever.
  return [first, { a: first.a === 9 ? 2 : first.a + 1, b: first.b }];
}

export function sumAnswered(s: Sum, answer: string): boolean {
  return answer.trim() !== "" && Number(answer.trim()) === s.a * s.b;
}

export type DeleteGates = {
  typed: string;
  consent: boolean;
  sums: [Sum, Sum];
  answers: [string, string];
};

export function canConfirmDelete(p: DeleteProjectPreview, g: DeleteGates): boolean {
  if (p.protected) return false;
  return (
    g.typed.trim() === p.name.trim() &&
    g.consent &&
    sumAnswered(g.sums[0], g.answers[0]) &&
    sumAnswered(g.sums[1], g.answers[1])
  );
}
