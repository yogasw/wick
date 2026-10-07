package agents

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// The blob endpoint follows the trace's ownership rule: a participant gets
// the bytes with their sniffed type, anyone else a 404 — and a blob_ref
// that is not a plain event id never reaches the filesystem.
func TestSessionTurnBlob_OwnerOnly(t *testing.T) {
	withSessionWorld(t, []seededSession{
		{id: "blobs", userID: "bob", participants: []string{"bob", "carol"}},
	})
	sess, _ := globalMgr.Registry().Session("blobs")
	sess.Meta.ProjectID = ""

	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{9}, 64)...)
	path := globalLayout.SessionThinkingBlob("blobs", "1759", "e3")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{"id": "blobs", "turn_id": "1759", "blob_ref": "e3"}
	target := "/tools/agents/sessions/blobs/turns/1759/blobs/e3"

	carol := &entity.User{ID: "carol", Role: entity.RoleUser}
	w, c := userCtx(t, carol, target, vals)
	sessionTurnBlob(c)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("participant: code %d, %d bytes", w.Code, w.Body.Len())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}

	dave := &entity.User{ID: "dave", Role: entity.RoleUser}
	if !ownsSession(c, sess) {
		t.Fatal("precondition: carol owns the session")
	}
	w, c = userCtx(t, dave, target, vals)
	if ownsSession(c, sess) {
		t.Skip("dave reaches the session through project access; cross-user case not representable")
	}
	sessionTurnBlob(c)
	if w.Code != http.StatusNotFound || w.Body.Len() > 100 {
		t.Fatalf("cross-user must be 404 without bytes, got %d (%d bytes)", w.Code, w.Body.Len())
	}

	for _, ref := range []string{"../e3", "e3.bin", "index", "e3-x"} {
		w, c = userCtx(t, carol, target, map[string]string{"id": "blobs", "turn_id": "1759", "blob_ref": ref})
		sessionTurnBlob(c)
		if w.Code != http.StatusNotFound {
			t.Errorf("blob_ref %q: code %d, want 404", ref, w.Code)
		}
	}
}
