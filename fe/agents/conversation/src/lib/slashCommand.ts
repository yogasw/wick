/* A bare slash command — "/compact", "/context", "/usage" — is a control
   instruction, not something a person said. The backend recognises the
   same shape (agents/store.IsBareSlashCommand) so it can skip the sender
   line and let the CLI actually run the command; the thread recognises it
   so the message reads as a command instead of a chat bubble. The two
   definitions must stay in step, so keep this one as strict as the Go one:
   a single line, a leading slash, and nothing but command characters. */
const BARE = /^\/([A-Za-z0-9_:-]+)$/;

/** The command name without its slash ("compact"), or "" when the text is
 *  an ordinary message. */
export function bareSlashCommand(text: string | undefined | null): string {
  const t = (text ?? "").trim();
  if (!t.startsWith("/") || t.includes("\n")) return "";
  const m = BARE.exec(t);
  return m ? m[1] : "";
}
