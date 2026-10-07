import type { Delivery } from "./types/agents.js";

/* A link back to the Slack message, or "" when there is none worth
   rendering. Only http(s): the URL comes from the server, but an href is
   the one place a stray `javascript:` would turn into a click. */
export function jumpLink(url: string | undefined | null): string {
  const u = (url ?? "").trim();
  return /^https?:\/\//i.test(u) ? u : "";
}

export type DeliveryView = {
  state: "sending" | "sent" | "failed";
  label: string;
  link: string;
};

/* What the bubble says about an assistant reply's trip to Slack. The point
   is proof: "Sent" only when Slack accepted it, the reason when it did not.
   null for a turn no channel posted, or a status this build does not know. */
export function deliveryView(d: Delivery | undefined | null): DeliveryView | null {
  if (!d) return null;
  const where = d.channel === "slack" || !d.channel ? "Slack" : d.channel.charAt(0).toUpperCase() + d.channel.slice(1);
  const link = jumpLink(d.permalink);
  switch (d.status) {
    case "sending":
      return { state: "sending", label: `Sending to ${where}…`, link: "" };
    case "sent":
      return { state: "sent", label: `Sent to ${where}`, link };
    case "failed":
      return { state: "failed", label: `Not sent to ${where}` + (d.error ? `: ${d.error}` : ""), link };
  }
  return null;
}
