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
	InitText string `wick:"desc=Seed text dropped into the input textarea on first load.;required"`

	// InitType is the conversion type pre-selected on first load.
	// Dropdown options are pinned at module boot.
	InitType string `wick:"desc=Seed conversion type pre-selected on first load.;dropdown=uppercase|lowercase|titlecase|sentencecase|alternating"`
}
