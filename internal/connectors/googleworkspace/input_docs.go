package googleworkspace

// Docs input structs — one per operation.

// DocsAppendTextInput is the argument schema for docs_append_text.
type DocsAppendTextInput struct {
	FileID string `wick:"required;desc=Google Document file ID."`
	Text   string `wick:"required;textarea;desc=Plain text to append at the end of the document."`
}

// DocsReplaceTextInput is the argument schema for docs_replace_text.
type DocsReplaceTextInput struct {
	FileID    string `wick:"required;desc=Google Document file ID."`
	Find      string `wick:"required;desc=Text to search for throughout the document."`
	Replace   string `wick:"required;desc=Text to substitute in place of every match."`
	MatchCase bool   `wick:"desc=Case-sensitive match. Default: false (case-insensitive)."`
}
