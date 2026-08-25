package converttext

// Config is the typed, runtime-editable configuration for a
// convert-text instance. Each exported field with a `wick:"..."` tag
// becomes one row in the `configs` table, scoped to this instance's
// Meta.Key. Admin UI widgets are picked from the Go type + tag flags.
//
// See entity.StructToConfigs for the full tag grammar; here is a
// short reference:
//
//	desc=...          → field description (shown in admin UI)
//	required          → block tool via c.Missing() until set
//	secret            → mask value in UI, redact in responses
//	locked            → operator-only (UI renders read-only)
//	regen             → "Regenerate" button in UI (pairs w/ generator)
//	textarea          → multi-line input
//	dropdown=a|b|c    → select with the given pipe-separated options
//	number            → numeric input (auto from int/float)
//	checkbox          → bool toggle (auto from bool)
//	email | url | color | date | datetime → typed input widgets
//	key=custom_name   → override default snake_case key
type Config struct {
	// InitText is the seed value dropped into the input textarea on
	// first load. Empty = blank textarea.
	InitText string `wick:"desc=Seed text dropped into the input textarea on first load."`

	// InitType is the conversion type pre-selected on first load.
	// Dropdown options are pinned at module boot.
	InitType string `wick:"desc=Seed conversion type pre-selected on first load.;dropdown=uppercase|lowercase|titlecase|sentencecase|alternating"`

	// WebhookEnabled gates the tool's unauthenticated JSON endpoint. The
	// route is always mounted — a Go route table cannot change at runtime —
	// so the handler reads this on every request and refuses when it is off.
	//
	// Default off. An endpoint that answers without a login should be
	// switched on deliberately by an admin who knows it is exposed, not
	// arrive open because a tool happened to be installed.
	WebhookEnabled bool `wick:"bool;desc=Allow POST /webhook/convert to be called without a login. Off by default — turn on only if an external system needs it, and set a signing secret below."`

	// WebhookSecret is the HMAC key for the endpoint above.
	//
	// Not marked required: the tool works fine with the webhook off, and a
	// required-but-empty row would show a permanent "setup required" banner
	// on an instance that never wanted a webhook. The handler enforces it
	// instead — no secret means no deliveries, even when enabled.
	WebhookSecret string `wick:"secret;desc=Signing key for the webhook. The sender computes HMAC-SHA256 over the raw request body and sends it hex-encoded in the X-Hook-Signature header. Deliveries are refused while this is empty."`
}
