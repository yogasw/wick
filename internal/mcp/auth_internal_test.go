package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

type internalStubUsers struct{}

func (internalStubUsers) GetUserByID(context.Context, string) (*entity.User, error) {
	return nil, errors.New("no db in test")
}
func (internalStubUsers) GetUserFilterTagIDs(context.Context, string) []string { return nil }

func TestAuthMiddleware_InternalToken(t *testing.T) {
	m := NewAuthMiddleware(nil, internalStubUsers{}, nil, "").WithInternalToken("sek-ret-123")

	var called, gotAdmin bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		u := login.GetUser(r.Context())
		gotAdmin = u != nil && u.IsAdmin()
		w.WriteHeader(http.StatusOK)
	})
	h := m.Wrap(next)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer sek-ret-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called || !gotAdmin {
		t.Fatalf("internal token: code=%d called=%v admin=%v", rec.Code, called, gotAdmin)
	}

	called = false
	req2 := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req2.Header.Set("Authorization", "Bearer wrong-token")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized || called {
		t.Fatalf("wrong token: code=%d called=%v", rec2.Code, called)
	}
}
