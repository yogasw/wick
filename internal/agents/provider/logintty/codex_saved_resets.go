package logintty

// Codex saved resets ("banked" rate-limit reset credits).
//
// Codex's own usage comes from its rollout journal (codex_usage.go),
// which does not carry the credits, so they are read from the ChatGPT
// account API with the instance's own login (CODEX_HOME/auth.json):
//
//   - GET …/wham/usage → rate_limit_reset_credits.available_count
//   - GET …/wham/rate-limit-reset-credits → credits[] (expiry per credit),
//     asked only when the count is above zero.
//
// Both run inside the paced usage probe, never per page view.

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/savedresets"
)

// Codex's reader registers itself; nothing outside this file knows it.
func init() {
	savedresets.Register(string(provider.TypeCodex), readCodexSavedResets)
}

// chatgptResetCreditsURL is swapped in tests.
var chatgptResetCreditsURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"

const codexSavedResetsHint = "A reset refills the 5-hour and weekly limits and moves the weekly reset day. Use it from Codex."

// codexAuthEntry reads the ChatGPT login of a codex config dir in the
// shape chatgptGet takes. The token never leaves this package.
func codexAuthEntry(dir string) (opencodeAuthEntry, error) {
	var auth struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if dir == "" || !readJSON(filepath.Join(dir, "auth.json"), &auth) || auth.Tokens.AccessToken == "" {
		return opencodeAuthEntry{}, errors.New("no ChatGPT login")
	}
	e := opencodeAuthEntry{Type: "oauth", Access: auth.Tokens.AccessToken, AccountID: auth.Tokens.AccountID}
	if claims, ok := decodeJWTClaims(e.Access); ok {
		if exp, _ := claims["exp"].(float64); exp > 0 {
			e.Expires = int64(exp) * 1000
		}
	}
	return e, nil
}

// readCodexSavedResets is Codex's registered savedresets.Reader.
func readCodexSavedResets(env []string) (*savedresets.SavedResets, error) {
	e, err := codexAuthEntry(codexConfigDir(env))
	if err != nil {
		return nil, err
	}
	body, err := chatgptGet(chatgptUsageURL, e)
	if err != nil {
		return nil, err
	}
	out, err := parseCodexResetCount(body)
	if err != nil || out.Available == 0 {
		return out, err
	}
	// The list only adds expiry dates; failing it keeps the count.
	if list, lerr := chatgptGet(chatgptResetCreditsURL, e); lerr == nil {
		if items, perr := parseCodexResetCredits(list); perr == nil {
			out.Items = items
		}
	}
	return out, nil
}

// parseCodexResetCount reads rate_limit_reset_credits off /wham/usage.
// An answer without the block means the account is not offered resets.
func parseCodexResetCount(body []byte) (*savedresets.SavedResets, error) {
	var p struct {
		Credits *struct {
			AvailableCount *int `json:"available_count"`
		} `json:"rate_limit_reset_credits"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("usage endpoint: %w", err)
	}
	if p.Credits == nil || p.Credits.AvailableCount == nil {
		return &savedresets.SavedResets{Supported: true, Note: "Not available for this account."}, nil
	}
	return &savedresets.SavedResets{Supported: true, Available: max(*p.Credits.AvailableCount, 0), Hint: codexSavedResetsHint}, nil
}

// codexSpentStatuses are credit states that can no longer be spent.
var codexSpentStatuses = map[string]bool{"consumed": true, "redeemed": true, "used": true, "expired": true, "revoked": true}

// parseCodexResetCredits reads the credits list, keeping spendable ones.
func parseCodexResetCredits(body []byte) ([]savedresets.Reset, error) {
	var p struct {
		Credits []struct {
			ID        string `json:"id"`
			ResetType string `json:"reset_type"`
			Status    string `json:"status"`
			GrantedAt any    `json:"granted_at"`
			ExpiresAt any    `json:"expires_at"`
			Title     string `json:"title"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("reset credits: %w", err)
	}
	now := time.Now()
	var out []savedresets.Reset
	for _, c := range p.Credits {
		if codexSpentStatuses[strings.ToLower(c.Status)] {
			continue
		}
		exp := savedresets.ParseTime(c.ExpiresAt)
		if !exp.IsZero() && exp.Before(now) {
			continue
		}
		label := strings.TrimSpace(c.Title)
		if label == "" {
			label = strings.ReplaceAll(strings.TrimSpace(c.ResetType), "_", " ")
		}
		out = append(out, savedresets.Reset{
			ID:        c.ID,
			Label:     label,
			StartsAt:  savedresets.ParseTime(c.GrantedAt),
			ExpiresAt: exp,
			UsableNow: true,
		})
	}
	savedresets.SortItems(out)
	return out, nil
}
