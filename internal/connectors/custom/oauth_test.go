package custom

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newOAuthFixture stands up a fake MCP resource + authorization server
// pair: the MCP URL 401s with a resource_metadata pointer, the AS
// serves RFC 8414 metadata, RFC 7591 registration, and a PKCE-checking
// token endpoint.
func newOAuthFixture(t *testing.T) (svc *Service, mcpURL string, tokenCalls *[]url.Values) {
	t.Helper()
	calls := &[]url.Values{}

	mux := http.NewServeMux()
	var asURL string
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	asURL = srv.URL

	// MCP endpoint — gated, points at the resource metadata.
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer at-live" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"ping","description":"Ping."}]}}`)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+asURL+`/.well-known/oauth-protected-resource/mcp"`)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_token"}`)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"resource":              asURL + "/",
			"authorization_servers": []string{asURL},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 asURL,
			"authorization_endpoint": asURL + "/authorize",
			"token_endpoint":         asURL + "/token",
			"registration_endpoint":  asURL + "/register",
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if uris, _ := req["redirect_uris"].([]any); len(uris) == 0 {
			http.Error(w, "redirect_uris required", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"client_id": "dcr-client"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		*calls = append(*calls, r.PostForm)
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			if r.PostForm.Get("code") != "code-1" || r.PostForm.Get("code_verifier") == "" {
				http.Error(w, "bad code", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-live", "refresh_token": "rt-1", "expires_in": 3600})
		case "refresh_token":
			if r.PostForm.Get("refresh_token") != "rt-1" {
				http.Error(w, "bad refresh", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-fresh", "expires_in": 3600})
		default:
			http.Error(w, "bad grant", http.StatusBadRequest)
		}
	})

	svc = New(Deps{})
	return svc, srv.URL + "/mcp", calls
}

// TestOAuthLoginRoundTrip drives discovery → dynamic registration →
// PKCE exchange → probe-with-token, asserting the verifier matches the
// challenge the authorization URL carried.
func TestOAuthLoginRoundTrip(t *testing.T) {
	svc, mcpURL, tokenCalls := newOAuthFixture(t)
	ctx := context.Background()
	redirect := "http://wick.local/cb"

	authURL, loginID, err := svc.StartOAuthLogin(ctx, &ServerForm{Label: "x", URL: mcpURL, AuthScheme: "oauth"}, redirect, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	q := u.Query()
	if q.Get("client_id") != "dcr-client" {
		t.Errorf("client_id = %q, want dcr-client (dynamic registration)", q.Get("client_id"))
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Errorf("PKCE challenge missing: %v", q)
	}
	state := q.Get("state")
	if state == "" {
		t.Fatal("state missing from authorization URL")
	}
	if q.Get("resource") == "" || !strings.HasSuffix(q.Get("resource"), "/") {
		t.Errorf("resource indicator missing from authorization URL: %q", q.Get("resource"))
	}

	res, err := svc.CompleteOAuthLogin(ctx, state, "code-1", redirect)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if res.LoginID != loginID {
		t.Errorf("login id mismatch: %q vs %q", res.LoginID, loginID)
	}

	// Verifier must hash to the challenge from the auth URL.
	exchange := (*tokenCalls)[0]
	sum := sha256.Sum256([]byte(exchange.Get("code_verifier")))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != q.Get("code_challenge") {
		t.Errorf("verifier does not match challenge")
	}
	if exchange.Get("resource") != q.Get("resource") {
		t.Errorf("token exchange resource %q must match authorization resource %q", exchange.Get("resource"), q.Get("resource"))
	}

	// The completed login powers a test probe.
	probe := svc.TestServer(ctx, &ServerForm{Label: "x", URL: mcpURL, AuthScheme: "oauth", OAuthLoginID: loginID}, nil)
	if !probe.OK || len(probe.Tools) != 1 {
		t.Fatalf("probe with oauth token: %+v", probe)
	}

	// Without a login the probe asks the UI to start one.
	needs := svc.TestServer(ctx, &ServerForm{Label: "x", URL: mcpURL, AuthScheme: "oauth"}, nil)
	if needs.OK || !needs.NeedsLogin {
		t.Fatalf("expected NeedsLogin, got %+v", needs)
	}
}

func TestOAuthRefreshTokens(t *testing.T) {
	svc, mcpURL, _ := newOAuthFixture(t)
	ctx := context.Background()
	meta, reg, err := svc.discoverOAuth(ctx, mcpURL)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if reg == "" {
		t.Fatal("registration endpoint missing from discovery")
	}
	meta.ClientID = "dcr-client"
	fresh, err := svc.refreshTokens(ctx, meta, "rt-1")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if fresh.AccessToken != "at-fresh" {
		t.Errorf("access = %q", fresh.AccessToken)
	}
	if fresh.RefreshToken != "rt-1" {
		t.Errorf("refresh token must survive when the server omits it, got %q", fresh.RefreshToken)
	}
}

// TestParamFromAuthHeader covers the WWW-Authenticate parsing edge
// cases the discovery path leans on.
func TestParamFromAuthHeader(t *testing.T) {
	cases := []struct{ header, want string }{
		{`Bearer resource_metadata="https://x/.well-known/oauth-protected-resource"`, "https://x/.well-known/oauth-protected-resource"},
		{`Bearer error="invalid_token", resource_metadata="https://y/meta", scope="a"`, "https://y/meta"},
		{`Bearer error="invalid_token"`, ""},
	}
	for _, c := range cases {
		if got := paramFromAuthHeader(c.header, "resource_metadata"); got != c.want {
			t.Errorf("header %q: got %q want %q", c.header, got, c.want)
		}
	}
	if !strings.Contains(cases[0].header, "resource_metadata") {
		t.Fatal("sanity")
	}
}
