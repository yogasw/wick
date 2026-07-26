package main

import "testing"

// TestBrowserStatusConfigOnly guards that browser_status stays a
// config-form widget renderer, hidden from the MCP tool surface. It
// returns HTML (for the manager's browser picker), so exposing it as a
// normal op leaked an HTML document to any agent that called it. Any
// data ops (page tasks, installs) must stay MCP-visible.
func TestBrowserStatusConfigOnly(t *testing.T) {
	var found bool
	for _, op := range Module().AllOps() {
		if op.Key == "browser_status" {
			found = true
			if !op.ConfigOnly {
				t.Errorf("browser_status must be ConfigOnly (HTML widget renderer), " +
					"else it leaks HTML onto the MCP tool surface")
			}
		}
	}
	if !found {
		t.Fatal("browser_status op not found in module")
	}
}
