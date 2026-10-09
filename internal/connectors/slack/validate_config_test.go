package slack

import "testing"

func TestValidateConfigTokenPrefixes(t *testing.T) {
	cases := []struct {
		key, value string
		ok         bool
	}{
		{"bot_token", "xoxb-123-abc", true},
		{"bot_token", "  xoxb-123-abc  ", true},
		{"bot_token", "xoxp-123-abc", false},
		{"bot_token", "garbage", false},
		{"user_token", "xoxp-123-abc", true},
		{"user_token", "xoxb-123-abc", false},
		// Token rotation prefixes the access token with "xoxe.".
		{"bot_token", "xoxe.xoxb-1-abc", true},
		{"user_token", "xoxe.xoxp-1-abc", true},
		{"bot_token", "xoxe.xoxp-1-abc", false},
		{"bot_token", "xoxe-1-refresh", false},
		{"auth_mode", "bot_token", true},
		{"client_id", "123.456", true},
	}
	for _, tc := range cases {
		err := ValidateConfig(tc.key, tc.value)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateConfig(%q, %q) err = %v, want ok=%v", tc.key, tc.value, err, tc.ok)
		}
	}
}
