package entity

// DefaultTag is the spec used by a module's DefaultTags to seed tags on
// startup. It lives in pkg/entity (a leaf with no UI/templ deps) so that
// pkg/connector and pkg/job can reference it WITHOUT importing pkg/tool —
// pkg/tool pulls in the HTML render stack (templ), which has no business in a
// connector plugin binary. pkg/tool keeps a `DefaultTag = entity.DefaultTag`
// alias for backward compatibility.
//
// IsSystem marks the tag as code-owned — see Tag godoc for the admin-UI
// implications (cannot be assigned to users from the picker).
type DefaultTag struct {
	Name        string
	Description string
	IsGroup     bool
	IsFilter    bool
	IsSystem    bool
	SortOrder   int
}
