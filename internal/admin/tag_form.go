package admin

import (
	"mime"
	"net/http"
	"strings"
)

// Reading a tag form, defensively.
//
// This exists because of a real data loss: the background saver posted
// its body as multipart/form-data, Go's r.ParseForm() does not parse a
// multipart body at all, and so the handlers read ZERO tag ids from a
// submission that carried several — then saved zero, wiping the row.
// Every tag write is a replace ("these are now the tags"), so a body the
// server cannot read looks exactly like "the user cleared them".
//
// Two rules make that unrepeatable:
//
//  1. Parse BOTH encodings. Whatever the client sends, the ids come out.
//  2. Require a marker field that every tag form renders. Clearing tags
//     is a legitimate operation and must keep working, so "no tag_ids[]"
//     cannot be treated as an error — but "no marker either" means the
//     body was not a tag form we understand, and THAT is refused instead
//     of being written as an empty list.
const tagSubmitMarker = "tags_submitted"

// tagIDsFromForm returns the submitted tag ids. ok=false means the
// submission was not readable as a tag form; the caller must write
// NOTHING and answer an error.
func tagIDsFromForm(r *http.Request) (ids []string, ok bool) {
	ct := r.Header.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(ct); err == nil && strings.HasPrefix(mt, "multipart/") {
		// 1 MiB is far beyond any list of tag ids; the point is only to
		// populate r.Form, which ParseForm alone would leave empty.
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, false
		}
	} else if err := r.ParseForm(); err != nil {
		return nil, false
	}
	if r.Form.Get(tagSubmitMarker) == "" {
		return nil, false
	}
	return dedupNonEmpty(r.Form["tag_ids[]"]), true
}

// refuseUnreadableTagForm answers a submission we could not read. The
// message names the fix, because the likely cause is a page loaded
// before the marker existed.
func refuseUnreadableTagForm(w http.ResponseWriter) {
	http.Error(w, "tag form not recognised — reload the page and save again (nothing was changed)", http.StatusBadRequest)
}
