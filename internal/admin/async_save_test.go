package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A normal form post keeps the redirect it always had — that is the
// no-JavaScript path, and breaking it would make the admin pages
// unusable in exactly the situations where you most need them.
func TestRedirectOrNoContentRedirectsPlainForm(t *testing.T) {
	rec := httptest.NewRecorder()
	redirectOrNoContent(rec, httptest.NewRequest("POST", "/admin/users/u1/tags", nil), "/admin/users")

	if rec.Code != http.StatusFound {
		t.Errorf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/users" {
		t.Errorf("Location = %q", loc)
	}
}

// The background saver gets 204: no body to parse, no page to re-render,
// and nothing that would scroll the operator back to the top of a long
// list they are working down.
func TestRedirectOrNoContentAnswers204ForAsyncSave(t *testing.T) {
	r := httptest.NewRequest("POST", "/admin/users/u1/tags", nil)
	r.Header.Set(asyncSaveHeader, "1")
	rec := httptest.NewRecorder()
	redirectOrNoContent(rec, r, "/admin/users")

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q, want none — the page is not navigating", loc)
	}
}

func TestWantsAsyncSaveReadsOnlyItsOwnHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/x", nil)
	if wantsAsyncSave(r) {
		t.Error("a bare request must not be treated as an async save")
	}
	// A stray XHR header from something else must not silently change
	// the response shape of a form post.
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	if wantsAsyncSave(r) {
		t.Error("X-Requested-With must not count — only the explicit marker does")
	}
	r.Header.Set(asyncSaveHeader, "1")
	if !wantsAsyncSave(r) {
		t.Error("explicit marker not detected")
	}
}

// The admin panel builds the provider tag paths by hand; the agents
// package reads them. They are one contract written twice, so pin the
// literals here too — see TestProviderTagPaths on the other side.
func TestProviderTagPathsMatchTheReaderSide(t *testing.T) {
	if got := providerAccessTagPath("claude", "engineer"); got != "/providers/claude/engineer" {
		t.Errorf("access path = %q", got)
	}
	if got := providerManageTagPath("claude", "engineer"); got != "/providers/claude/engineer/manage" {
		t.Errorf("manage path = %q", got)
	}
}
