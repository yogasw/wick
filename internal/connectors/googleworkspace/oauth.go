// Package googleworkspace — oauth.go: OAuthMeta implementation for the Google Workspace connector.
//
// Purpose: Provides the OAuthMeta descriptor that wires Google OAuth2 into the
// generic manager OAuth framework. Uses offline access to obtain a refresh_token
// that enables auto-renewal when the 1-hour access token expires.
//
// Caller:   OAuthMeta() referenced from internal/connectors/registry.go
// Dependencies: standard net/http, encoding/json
package googleworkspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/yogasw/wick/pkg/connector"
)

// OAuthMeta returns the OAuthMeta descriptor for Google Drive user token OAuth.
func OAuthMeta() *connector.OAuthMeta {
	return &connector.OAuthMeta{
		AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		ExtraParams: map[string]string{
			"response_type": "code",
			"access_type":   "offline",
			"prompt":        "consent",
		},
		Scopes:      "https://www.googleapis.com/auth/drive https://www.googleapis.com/auth/userinfo.email",
		DisplayName: "Google Drive",
		Icon:        "📁",
		GetUserIdentity: func(ctx context.Context, accessToken string) (string, string, error) {
			return fetchUserInfo(ctx, accessToken)
		},
	}
}

func fetchUserInfo(ctx context.Context, accessToken string) (userID, email string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return "", "", fmt.Errorf("build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("userinfo request: %w", err)
	}
	defer resp.Body.Close()

	var info struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", "", fmt.Errorf("decode userinfo: %w", err)
	}
	if info.ID == "" {
		return "", "", fmt.Errorf("empty user ID from Google userinfo")
	}
	return info.ID, info.Email, nil
}
