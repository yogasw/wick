package host

import "net/http"

// CallbackPrefix is the /x/{key} mount of the callback API ("-" is never a
// plugin key); Route.Pattern is relative to it.
const CallbackPrefix = "/x/-"

// RestPrefix is where the Rest RPC lives on the HTTP door: the wick REST path
// follows it verbatim (ANY /x/-/api/rest/api/tickets/42).
const RestPrefix = "/api/rest"

// Route maps one Host RPC onto the HTTP door. {name} segments are filled from
// the request field of that proto name. GET requests send their remaining
// populated fields as query parameters (proto names); POST/PATCH send the
// request as an HTTP door body (MarshalHTTP: plain JSON, *_json fields
// inline). Success bodies are the response in the same codec.
type Route struct {
	RPC     string
	Method  string
	Pattern string
}

// Routes is the HTTP door table, shared by this client and wick's server so
// both sides agree on paths. Rest is not listed: it is ANY RestPrefix + path,
// a raw passthrough (request and response bodies/headers as-is).
//
// Whoami compatibility: GET /api/whoami already answers service plugins with
// {"plugin","scopes"}. The server MUST keep sending those two keys next to the
// WhoamiResponse fields in the same body (a superset), so old plugins keep
// working; this client reads either shape.
var Routes = []Route{
	// --- meta (whoami, always allowed) ---
	{"Whoami", http.MethodGet, "/api/whoami"},
	{"ListMethods", http.MethodGet, "/api/methods"},

	// --- users.read ---
	{"ListUsers", http.MethodGet, "/api/users"},
	{"GetUser", http.MethodGet, "/api/users/{id}"},
	{"GetUserByEmail", http.MethodGet, "/api/users/by-email/{email}"},

	// --- accounts.read ---
	{"ConnectedAccounts", http.MethodGet, "/api/accounts"},

	// --- sessions.read / sessions.notify ---
	{"ListSessions", http.MethodGet, "/api/sessions"},
	{"GetSessionMeta", http.MethodGet, "/api/sessions/{id}"},
	{"SendToSession", http.MethodPost, "/api/sessions/{session_id}/send"},

	// --- tickets.read / tickets.write ---
	{"GetTicket", http.MethodGet, "/api/tickets/{id}"},
	{"ListTickets", http.MethodGet, "/api/tickets"},
	{"CreateTicket", http.MethodPost, "/api/tickets"},
	{"UpdateTicket", http.MethodPatch, "/api/tickets/{id}"},
	{"AttachSession", http.MethodPost, "/api/tickets/{ticket_id}/sessions"},

	// --- config.read_other ---
	{"GetConfig", http.MethodGet, "/api/config"},

	// --- connectors.read / connectors.execute ---
	{"ListConnectorInstances", http.MethodGet, "/api/connectors"},
	{"ExecuteConnector", http.MethodPost, "/api/connectors/execute"},

	// --- generic layer (Rest: see RestPrefix) ---
	{"Invoke", http.MethodPost, "/api/invoke"},
	{"Query", http.MethodPost, "/api/query"},
}

var routeByRPC = func() map[string]Route {
	m := make(map[string]Route, len(Routes))
	for _, r := range Routes {
		m[r.RPC] = r
	}
	return m
}()
