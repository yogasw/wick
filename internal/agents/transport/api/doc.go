// Package api will hold the HTTP API transport — external integrations
// that POST messages directly into a session, bypassing UI / Slack.
//
// Out of scope MVP — placeholder folder so the transport sibling
// layout is complete for future work. Add only when there is a real
// downstream consumer; until then this folder is just a marker.
//
// Reference (agents-design.md §4.7):
//
//   - Source: "api"
//   - SessionKey: UUID
//   - Auth: wick API token / OAuth (TBD when implemented)
package api
