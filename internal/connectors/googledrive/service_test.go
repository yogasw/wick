package googledrive

import (
	"strings"
	"testing"
)

func TestNormalizeGranted(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantTrue []string
	}{
		{
			name:  "drive implies readonly",
			input: "https://www.googleapis.com/auth/drive",
			wantTrue: []string{
				"https://www.googleapis.com/auth/drive",
				"https://www.googleapis.com/auth/drive.readonly",
			},
		},
		{
			name:     "readonly does not imply drive",
			input:    "https://www.googleapis.com/auth/drive.readonly",
			wantTrue: []string{"https://www.googleapis.com/auth/drive.readonly"},
		},
		{
			name:  "multiple scopes parsed",
			input: "https://www.googleapis.com/auth/drive https://www.googleapis.com/auth/userinfo.email",
			wantTrue: []string{
				"https://www.googleapis.com/auth/drive",
				"https://www.googleapis.com/auth/drive.readonly",
				"https://www.googleapis.com/auth/userinfo.email",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeGranted(tc.input)
			for _, s := range tc.wantTrue {
				if !got[s] {
					t.Errorf("scope %q should be granted but is not", s)
				}
			}
		})
	}
}

func TestEvalScopes(t *testing.T) {
	tests := []struct {
		name     string
		required [][]string
		granted  map[string]bool
		wantOK   bool
	}{
		{
			name:     "read op satisfied by drive.readonly",
			required: opScopes["list_files"],
			granted:  map[string]bool{"https://www.googleapis.com/auth/drive.readonly": true},
			wantOK:   true,
		},
		{
			name:     "read op satisfied by drive (via normalizeGranted implication)",
			required: opScopes["list_files"],
			granted:  normalizeGranted("https://www.googleapis.com/auth/drive"),
			wantOK:   true,
		},
		{
			name:     "write op requires drive, denied with only readonly",
			required: opScopes["upload_file"],
			granted:  map[string]bool{"https://www.googleapis.com/auth/drive.readonly": true},
			wantOK:   false,
		},
		{
			name:     "write op satisfied by drive",
			required: opScopes["upload_file"],
			granted:  map[string]bool{"https://www.googleapis.com/auth/drive": true},
			wantOK:   true,
		},
		{
			name:     "no scopes",
			required: opScopes["list_files"],
			granted:  map[string]bool{},
			wantOK:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, missing := evalScopes(tc.required, tc.granted)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v; missing = %v", ok, tc.wantOK, missing)
			}
			if !tc.wantOK && len(missing) == 0 {
				t.Error("expected missing scopes, got none")
			}
		})
	}
}

func TestBuildListParams(t *testing.T) {
	t.Run("root folder", func(t *testing.T) {
		p := buildListParams("", 50, "modifiedTime desc")
		if p.Get("q") != "trashed=false" {
			t.Errorf("q = %q, want %q", p.Get("q"), "trashed=false")
		}
		if p.Get("pageSize") != "50" {
			t.Errorf("pageSize = %q, want 50", p.Get("pageSize"))
		}
		if p.Get("orderBy") != "modifiedTime desc" {
			t.Errorf("orderBy = %q", p.Get("orderBy"))
		}
	})
	t.Run("specific folder", func(t *testing.T) {
		p := buildListParams("folder123", 10, "name")
		if !strings.Contains(p.Get("q"), "folder123") {
			t.Errorf("q should contain folder123, got %q", p.Get("q"))
		}
	})
}

func TestBuildSearchParams(t *testing.T) {
	p := buildSearchParams("name contains 'report'", 25)
	if !strings.Contains(p.Get("q"), "name contains 'report'") {
		t.Errorf("q = %q", p.Get("q"))
	}
	if !strings.Contains(p.Get("q"), "trashed=false") {
		t.Errorf("q should exclude trashed, got %q", p.Get("q"))
	}
	if p.Get("pageSize") != "25" {
		t.Errorf("pageSize = %q, want 25", p.Get("pageSize"))
	}
}

func TestValidateString(t *testing.T) {
	t.Run("empty string returns error", func(t *testing.T) {
		_, err := validateString("", "file_id")
		if err == nil {
			t.Fatal("expected error for empty string")
		}
	})
	t.Run("whitespace-only returns error", func(t *testing.T) {
		_, err := validateString("   ", "file_id")
		if err == nil {
			t.Fatal("expected error for whitespace")
		}
	})
	t.Run("valid value trimmed", func(t *testing.T) {
		v, err := validateString("  abc123  ", "file_id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v != "abc123" {
			t.Errorf("v = %q, want %q", v, "abc123")
		}
	})
}
