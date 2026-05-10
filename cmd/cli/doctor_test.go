package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseGoVersion(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"go version go1.25.0 linux/amd64", "1.25.0"},
		{"go version go1.21.5 darwin/arm64", "1.21.5"},
		{"go version go1.22.0 windows/amd64", "1.22.0"},
		{"unexpected", "unexpected"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			assert.Equal(t, tt.want, parseGoVersion(tt.raw))
		})
	}
}

func TestLocalBinPath(t *testing.T) {
	p := localBinPath("templ")
	assert.Contains(t, p, "templ")
	assert.Contains(t, p, "bin")
}

func TestTailwindCandidates(t *testing.T) {
	candidates := tailwindCandidates()
	assert.NotEmpty(t, candidates)
	for _, c := range candidates {
		assert.Contains(t, c, "tailwindcss")
	}
}

func TestCollectChecksStructure(t *testing.T) {
	checks := collectChecks()

	// Must always have at minimum: wick CLI, go, wick.yml, templ, tailwindcss, MCP header
	assert.GreaterOrEqual(t, len(checks), 6)

	// First check is always wick CLI
	assert.Equal(t, "wick CLI", checks[0].label)
	assert.Equal(t, checkOK, checks[0].status)

	// Every check must have a label and a valid status
	validStatuses := map[string]bool{checkOK: true, checkFail: true, checkWarn: true}
	for _, c := range checks {
		assert.NotEmpty(t, c.label, "check label must not be empty")
		assert.True(t, validStatuses[c.status], "invalid status %q for %q", c.status, c.label)
	}
}

func TestDoctorCmdRegistered(t *testing.T) {
	cmd := doctorCmd()
	assert.Equal(t, "doctor", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotNil(t, cmd.RunE)
}
