package config

import (
	"strings"
	"testing"
)

// TestChannelAutoRegisterIsAgentLevel pins WHERE this switch lives.
//
// Creating a wick account is an install-level decision. Channel config rows are
// per-owner, so a per-channel toggle would let any user who adds their own Slack
// bot turn auto-registration on for it and mint pending wick accounts. Keeping
// it in the Agents settings means only an admin can allow that.
func TestChannelAutoRegisterIsAgentLevel(t *testing.T) {
	found := false
	for _, c := range SeedGeneralConfig() {
		if c.Key == "channel_auto_register" {
			found = true
			if c.Value == "true" {
				t.Error("ships ON; creating accounts from an inbound message must be opted into")
			}
			if !strings.Contains(strings.ToLower(c.Description), "pending approval") {
				t.Errorf("description must say the account is pending approval: %q", c.Description)
			}
		}
	}
	if !found {
		t.Fatal("channel_auto_register missing from the agents config seed")
	}
}

// TestSlackChannelHasNoAutoRegister guards the move: the per-channel key must
// not come back, or the per-owner escalation returns with it.
func TestSlackChannelHasNoAutoRegister(t *testing.T) {
	for _, c := range SeedSlackChannelConfig() {
		if strings.Contains(c.Key, "auto_register") {
			t.Fatalf("per-channel key %q is back; this switch belongs in the agents config", c.Key)
		}
	}
}
