package trigger

import "testing"

// "Text contains" is advertised in the slack.message / slack.thread_started
// filter form as `text_contains`, but no Slack payload carries a key by
// that name. Under plain equality the router compared the operator's text
// against "" and rejected every event — filling the field in silently
// turned the trigger off instead of narrowing it.
func TestMatchEventPayload_ContainsSuffix(t *testing.T) {
	payload := map[string]any{
		"channel_id": "C123",
		"text":       "Deploy FAILED on staging",
	}

	cases := []struct {
		name string
		spec map[string]any
		want bool
	}{
		{"substring hit", map[string]any{"text_contains": "failed"}, true},
		{"case-insensitive", map[string]any{"text_contains": "DEPLOY"}, true},
		{"substring miss", map[string]any{"text_contains": "rollback"}, false},
		{"empty is no filter", map[string]any{"text_contains": ""}, true},
		{"blank is no filter", map[string]any{"text_contains": "   "}, true},
		{"combined with picker key", map[string]any{
			"channel_id":    `[{"id":"C123","name":"#ops"}]`,
			"text_contains": "staging",
		}, true},
		{"combined, text rejects", map[string]any{
			"channel_id":    `[{"id":"C123","name":"#ops"}]`,
			"text_contains": "production",
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchEventPayload(tc.spec, payload); got != tc.want {
				t.Errorf("matchEventPayload(%v) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}

// A payload that really does carry the "_contains"-suffixed key keeps
// plain equality — the suffix rule must not shadow a genuine field.
func TestMatchEventPayload_ContainsSuffixIsRealKey(t *testing.T) {
	payload := map[string]any{"text_contains": "exact"}
	if !matchEventPayload(map[string]any{"text_contains": "exact"}, payload) {
		t.Error("declared payload key should match by equality")
	}
	if matchEventPayload(map[string]any{"text_contains": "exa"}, payload) {
		t.Error("declared payload key must not fall back to substring")
	}
}
