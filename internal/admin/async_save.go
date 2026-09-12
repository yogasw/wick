package admin

import "net/http"

// asyncSaveHeader is what the tag-picker's background save sends. Its
// presence is the ONLY signal used: a form posted by the browser (no JS,
// or JS that failed to load) never sets it and keeps the old redirect,
// so the page still works with scripting off.
const asyncSaveHeader = "X-Wick-Async"

// wantsAsyncSave reports whether this POST came from the background
// saver rather than a full-page form submit.
func wantsAsyncSave(r *http.Request) bool {
	return r.Header.Get(asyncSaveHeader) != ""
}

// redirectOrNoContent ends a small admin write.
//
// Historically every one of these ended in a 302 back to the list, which
// meant editing tags on ten users cost ten full page loads — and each
// reload threw away where the operator was on the page. The background
// saver posts the same form and gets 204 instead, so the row updates in
// place; anything else still gets the redirect it always got.
func redirectOrNoContent(w http.ResponseWriter, r *http.Request, path string) {
	if wantsAsyncSave(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, path, http.StatusFound)
}
