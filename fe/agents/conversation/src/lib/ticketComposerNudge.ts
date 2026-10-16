/* "+ New session" on a ticket page does not leave the page: the composer
   under the ticket already starts a session on it. The button only points
   at that composer — it scrolls into view, takes the cursor and plays a
   short slide-and-glow with a hint, so the first click shows where to type
   instead of seeming to do nothing. TicketComposer listens for the event. */
export const TICKET_COMPOSER_NUDGE = "wick:ticket-composer-nudge";

export function nudgeTicketComposer(): void {
  window.dispatchEvent(new CustomEvent(TICKET_COMPOSER_NUDGE));
}
