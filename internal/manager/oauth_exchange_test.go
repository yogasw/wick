package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExchangeGenericCode(t *testing.T) {
	h := &Handler{}

	tests := []struct {
		name        string
		response    map[string]any
		statusCode  int
		wantAccess  string
		wantRefresh string
		wantErr     bool
	}{
		{
			name:        "success with refresh token",
			response:    map[string]any{"access_token": "ya29.abc", "refresh_token": "1//xyz", "token_type": "Bearer"},
			statusCode:  http.StatusOK,
			wantAccess:  "ya29.abc",
			wantRefresh: "1//xyz",
		},
		{
			name:       "success without refresh token",
			response:   map[string]any{"access_token": "ya29.def", "token_type": "Bearer"},
			statusCode: http.StatusOK,
			wantAccess: "ya29.def",
		},
		{
			name:       "provider error field",
			response:   map[string]any{"error": "invalid_grant", "error_description": "Token has been expired or revoked."},
			statusCode: http.StatusOK,
			wantErr:    true,
		},
		{
			name:       "non-200 status",
			response:   map[string]any{"error": "bad_request"},
			statusCode: http.StatusBadRequest,
			wantErr:    true,
		},
		{
			name:       "empty access_token",
			response:   map[string]any{"token_type": "Bearer"},
			statusCode: http.StatusOK,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.statusCode)
				if tc.response != nil {
					json.NewEncoder(w).Encode(tc.response)
				}
			}))
			defer srv.Close()

			access, refresh, err := h.exchangeGenericCode(
				context.Background(), srv.URL, "client_id", "client_secret", "code123", "http://localhost/cb",
			)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if access != tc.wantAccess {
				t.Errorf("access_token = %q, want %q", access, tc.wantAccess)
			}
			if refresh != tc.wantRefresh {
				t.Errorf("refresh_token = %q, want %q", refresh, tc.wantRefresh)
			}
		})
	}
}
