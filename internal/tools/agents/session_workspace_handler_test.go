package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestMergeDraftConfig(t *testing.T) {
	specs := []entity.Config{
		{Key: "base_url"},
		{Key: "app_id"},
		{Key: "secret_key", IsSecret: true},
	}
	stored := map[string]string{
		"base_url":   "https://saved.example.com",
		"app_id":     "saved-app",
		"secret_key": "wick_cenc_saved",
	}

	cases := []struct {
		name  string
		typed map[string]string
		want  map[string]string
	}{
		{
			name:  "nil typed → stored unchanged",
			typed: nil,
			want:  stored,
		},
		{
			name:  "typed value overrides stored",
			typed: map[string]string{"base_url": "https://draft.example.com"},
			want: map[string]string{
				"base_url":   "https://draft.example.com",
				"app_id":     "saved-app",
				"secret_key": "wick_cenc_saved",
			},
		},
		{
			name:  "empty typed value keeps stored (leave-as-is)",
			typed: map[string]string{"base_url": "  ", "app_id": "draft-app"},
			want: map[string]string{
				"base_url":   "https://saved.example.com",
				"app_id":     "draft-app",
				"secret_key": "wick_cenc_saved",
			},
		},
		{
			name:  "unknown key ignored",
			typed: map[string]string{"not_a_field": "x", "app_id": "draft-app"},
			want: map[string]string{
				"base_url":   "https://saved.example.com",
				"app_id":     "draft-app",
				"secret_key": "wick_cenc_saved",
			},
		},
		{
			name:  "draft plaintext secret overlays the stored token",
			typed: map[string]string{"secret_key": "plaintext-draft"},
			want: map[string]string{
				"base_url":   "https://saved.example.com",
				"app_id":     "saved-app",
				"secret_key": "plaintext-draft",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeDraftConfig(stored, tc.typed, specs)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tc.want), got)
			}
			for k, want := range tc.want {
				if got[k] != want {
					t.Errorf("key %q = %q, want %q", k, got[k], want)
				}
			}
			// Must not mutate the caller's stored map.
			if stored["base_url"] != "https://saved.example.com" {
				t.Errorf("stored map was mutated: %v", stored)
			}
		})
	}
}
