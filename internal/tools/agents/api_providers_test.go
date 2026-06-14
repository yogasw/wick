package agents

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/tools/agents/view"
	pkgentity "github.com/yogasw/wick/pkg/entity"
)

func TestParseInt(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"0", 0},
		{"1", 1},
		{"42", 42},
		{"999", 999},
		{"", 0},
		{"abc", 0},
		{"-1", 0},
		{"1a", 0},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parseInt(tc.in)
			if got != tc.want {
				t.Errorf("parseInt(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestGateStatusDTO(t *testing.T) {
	cases := []struct {
		name string
		vm   view.GateStatusVM
	}{
		{
			name: "enabled gate",
			vm: view.GateStatusVM{
				Enabled:        true,
				Binary:         "/usr/local/bin/wick-gate",
				Source:         "sibling",
				Note:           "Gate is on.",
				PermissionMode: "on",
				BypassLocked:   false,
			},
		},
		{
			name: "bypass locked",
			vm: view.GateStatusVM{
				Enabled:        false,
				Binary:         "/usr/local/bin/wick-gate",
				Source:         "embed",
				Reason:         "bypass mode active",
				Note:           "Permission policy is bypass.",
				PermissionMode: "bypass",
				BypassLocked:   true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dto := gateStatusDTO(tc.vm)
			if dto.Enabled != tc.vm.Enabled {
				t.Errorf("Enabled: got %v, want %v", dto.Enabled, tc.vm.Enabled)
			}
			if dto.Binary != tc.vm.Binary {
				t.Errorf("Binary: got %q, want %q", dto.Binary, tc.vm.Binary)
			}
			if dto.PermissionMode != tc.vm.PermissionMode {
				t.Errorf("PermissionMode: got %q, want %q", dto.PermissionMode, tc.vm.PermissionMode)
			}
			if dto.BypassLocked != tc.vm.BypassLocked {
				t.Errorf("BypassLocked: got %v, want %v", dto.BypassLocked, tc.vm.BypassLocked)
			}
			if dto.Note != tc.vm.Note {
				t.Errorf("Note: got %q, want %q", dto.Note, tc.vm.Note)
			}
		})
	}
}

func TestHookCapabilityDTO(t *testing.T) {
	probed := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name         string
		cap          provider.HookCapability
		wantProbedAt string
	}{
		{
			name: "verified with probed_at",
			cap: provider.HookCapability{
				Supported: true,
				Verified:  true,
				ProbedAt:  probed,
				Scope:     "tool",
			},
			wantProbedAt: "2025-01-15T10:00:00Z",
		},
		{
			name: "zero probed_at omitted",
			cap: provider.HookCapability{
				Supported: false,
				Verified:  false,
				Error:     "binary not found",
			},
			wantProbedAt: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dto := hookCapabilityDTO(tc.cap)
			if dto.Supported != tc.cap.Supported {
				t.Errorf("Supported: got %v, want %v", dto.Supported, tc.cap.Supported)
			}
			if dto.Verified != tc.cap.Verified {
				t.Errorf("Verified: got %v, want %v", dto.Verified, tc.cap.Verified)
			}
			if dto.ProbedAt != tc.wantProbedAt {
				t.Errorf("ProbedAt: got %q, want %q", dto.ProbedAt, tc.wantProbedAt)
			}
			if dto.Error != tc.cap.Error {
				t.Errorf("Error: got %q, want %q", dto.Error, tc.cap.Error)
			}
		})
	}
}

func TestSpawnLogFileDTO(t *testing.T) {
	started := time.Date(2025, 3, 10, 8, 30, 0, 0, time.UTC)
	f := provider.SpawnLogFile{
		Path:             "/base/providers/spawns/claude__work__abc__123.jsonl",
		ProviderType:     "claude",
		ProviderName:     "work",
		SessionID:        "abc",
		StartedAt:        started,
		PID:              9876,
		Origin:           "slack",
		FirstUserMessage: "hello",
		Binary:           "/usr/bin/claude",
		ExitReason:       "clean",
	}
	dto := spawnLogFileDTO(f)

	if dto.ProviderType != "claude" {
		t.Errorf("ProviderType: got %q, want %q", dto.ProviderType, "claude")
	}
	if dto.ProviderName != "work" {
		t.Errorf("ProviderName: got %q, want %q", dto.ProviderName, "work")
	}
	if dto.StartedAt != "2025-03-10T08:30:00Z" {
		t.Errorf("StartedAt: got %q, want %q", dto.StartedAt, "2025-03-10T08:30:00Z")
	}
	if dto.PID != 9876 {
		t.Errorf("PID: got %d, want %d", dto.PID, 9876)
	}
	if dto.ExitReason != "clean" {
		t.Errorf("ExitReason: got %q, want %q", dto.ExitReason, "clean")
	}
}

func TestConfigFieldDTOs_SecretMasking(t *testing.T) {
	rows := []pkgentity.Config{
		{Key: "binary", Value: "/usr/bin/claude", Type: "text", IsSecret: false},
		{Key: "api_key", Value: "sk-secret-token-123", Type: "text", IsSecret: true},
		{Key: "empty_secret", Value: "", Type: "text", IsSecret: true},
	}
	dtos := configFieldDTOs(rows)

	if len(dtos) != 3 {
		t.Fatalf("len(dtos) = %d, want 3", len(dtos))
	}

	if dtos[0].Value != "/usr/bin/claude" {
		t.Errorf("non-secret value changed: got %q", dtos[0].Value)
	}
	if dtos[0].IsSecret {
		t.Error("binary should not be secret")
	}

	if dtos[1].Value != "••••••••" {
		t.Errorf("secret value not masked: got %q", dtos[1].Value)
	}
	if !dtos[1].IsSecret {
		t.Error("api_key should be marked secret")
	}

	if dtos[2].Value != "" {
		t.Errorf("empty secret should remain empty, got %q", dtos[2].Value)
	}
}

func TestMCPStatusDTO(t *testing.T) {
	vm := view.MCPStatusVM{
		AppName: "wick",
		Clients: []view.MCPClientStatusVM{
			{ID: "claude", Label: "Claude Desktop", Detected: true, Installed: true, Blocklisted: false},
			{ID: "cursor", Label: "Cursor", Detected: false, Installed: false, Blocklisted: true},
		},
	}
	dto := mcpStatusDTO(vm)

	if dto.AppName != "wick" {
		t.Errorf("AppName: got %q, want %q", dto.AppName, "wick")
	}
	if len(dto.Clients) != 2 {
		t.Fatalf("len(Clients) = %d, want 2", len(dto.Clients))
	}
	if dto.Clients[0].ID != "claude" || !dto.Clients[0].Installed {
		t.Errorf("first client unexpected: %+v", dto.Clients[0])
	}
	if dto.Clients[1].ID != "cursor" || !dto.Clients[1].Blocklisted {
		t.Errorf("second client unexpected: %+v", dto.Clients[1])
	}
}
