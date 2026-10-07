package admin

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

// The regression this guard exists for: the background saver posted
// multipart/form-data, ParseForm read nothing from it, and the handler
// saved an empty tag list over a row that had four tags.
func TestTagIDsFromFormReadsMultipartBody(t *testing.T) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField(tagSubmitMarker, "1")
	_ = mw.WriteField("tag_ids[]", "tag-a")
	_ = mw.WriteField("tag_ids[]", "tag-b")
	mw.Close()

	r := httptest.NewRequest("POST", "/admin/users/u1/tags", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())

	ids, ok := tagIDsFromForm(r)
	if !ok {
		t.Fatal("multipart submission rejected — this is the shape fetch() sends by default")
	}
	if len(ids) != 2 || ids[0] != "tag-a" || ids[1] != "tag-b" {
		t.Fatalf("ids = %v, want both tags — reading zero here is what wiped the rows", ids)
	}
}

func TestTagIDsFromFormReadsURLEncodedBody(t *testing.T) {
	form := url.Values{}
	form.Set(tagSubmitMarker, "1")
	form.Add("tag_ids[]", "tag-a")
	r := httptest.NewRequest("POST", "/admin/users/u1/tags", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ids, ok := tagIDsFromForm(r)
	if !ok || len(ids) != 1 || ids[0] != "tag-a" {
		t.Fatalf("ids = %v ok = %v", ids, ok)
	}
}

// Clearing every tag is a real operation and must keep working: the
// marker is present, the list is empty, and the handler writes empty.
func TestTagIDsFromFormAllowsAnIntentionalClear(t *testing.T) {
	form := url.Values{}
	form.Set(tagSubmitMarker, "1")
	r := httptest.NewRequest("POST", "/admin/users/u1/tags", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ids, ok := tagIDsFromForm(r)
	if !ok {
		t.Fatal("an explicit clear was refused")
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v, want empty", ids)
	}
}

// …but a body with no marker is NOT a clear. It is a body we failed to
// understand, and writing it would delete tags nobody asked to delete.
func TestTagIDsFromFormRefusesABodyItCannotRead(t *testing.T) {
	r := httptest.NewRequest("POST", "/admin/users/u1/tags", strings.NewReader("tag_ids%5B%5D=tag-a"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if _, ok := tagIDsFromForm(r); ok {
		t.Fatal("accepted a submission with no marker — an unreadable body must never be saved as an empty list")
	}
}
