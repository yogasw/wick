package custom

import (
	"strings"
	"testing"

	wfprovider "github.com/yogasw/wick/internal/agents/workflow/provider"
)

func TestAIParseRequestCarriesPasteAndSchema(t *testing.T) {
	req := AIParseRequest("fetch('https://api.example.com/x')")
	if !strings.Contains(req.Prompt, "https://api.example.com/x") || strings.Contains(req.Prompt, "{{PASTE}}") {
		t.Fatalf("prompt did not embed the paste: %q", req.Prompt)
	}
	if req.Schema == nil {
		t.Fatal("schema missing")
	}
}

func TestDraftFromAIResult(t *testing.T) {
	d, err := DraftFromAIResult(wfprovider.StructuredResult{OK: true, Parsed: map[string]any{
		"method": "post",
		"url":    "https://api.example.com/v1/items",
		"body":   `{"name":"demo"}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ops := d.AllOps()
	if len(ops) != 1 || ops[0].Request == nil || ops[0].Request.Method != "POST" {
		t.Fatalf("draft ops = %+v", ops)
	}
	if _, err := DraftFromAIResult(wfprovider.StructuredResult{OK: false, Error: "boom"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("failed result: %v", err)
	}
	if _, err := DraftFromAIResult(wfprovider.StructuredResult{OK: true, Parsed: map[string]any{"method": "GET"}}); err == nil {
		t.Fatal("missing url accepted")
	}
}

func TestCheckPasteSize(t *testing.T) {
	if CheckPasteSize(strings.Repeat("a", 8*1024)) != nil {
		t.Fatal("8 KB rejected")
	}
	if err := CheckPasteSize(strings.Repeat("a", 8*1024+1)); err == nil || !strings.Contains(err.Error(), "8 KB") {
		t.Fatalf("oversize: %v", err)
	}
}
