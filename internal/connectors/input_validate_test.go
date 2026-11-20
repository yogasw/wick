package connectors

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

func validateOp() connector.Operation {
	return connector.Operation{
		Key: "get_widget",
		Input: []entity.Config{
			{Key: "id", Type: "text", Required: true},
			{Key: "limit", Type: "number"},
			{Key: "live", Type: "checkbox"},
			{Key: "mode", Type: "dropdown", Options: "fast|slow"},
			{Key: "region", Type: "text", Required: true, Value: "eu"},
		},
	}
}

func TestValidateInputAcceptsGoodParams(t *testing.T) {
	cases := []map[string]any{
		{"id": "w1"},
		{"id": "w1", "limit": float64(5), "live": true, "mode": "fast"},
		{"id": "w1", "limit": "5", "live": "false", "mode": "slow"},
		{"id": "w1", "extra": "ignored"},
	}
	for i, raw := range cases {
		if err := ValidateInput(validateOp(), raw); err != nil {
			t.Fatalf("case %d: unexpected error %v", i, err)
		}
	}
}

func TestValidateInputReportsProblems(t *testing.T) {
	err := ValidateInput(validateOp(), map[string]any{
		"widget_id": "w1",
		"limit":     "five",
		"live":      "yes",
		"mode":      "medium",
	})
	if err == nil {
		t.Fatal("expected an InputError")
	}
	if len(err.Problems) != 4 {
		t.Fatalf("want 4 problems, got %d: %v", len(err.Problems), err.Problems)
	}
	if len(err.Unknown) != 1 || err.Unknown[0] != "widget_id" {
		t.Fatalf("unknown params = %v", err.Unknown)
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("message misses the required field: %s", err.Error())
	}
	if len(err.Inputs) != 5 {
		t.Fatal("inputs must ride along for the schema")
	}
}

func TestValidateInputOnlyForAgentSources(t *testing.T) {
	if !validatesInput(entity.ConnectorRunSourceMCP) || !validatesInput(entity.ConnectorRunSourceCodeMode) {
		t.Fatal("agent sources must be validated")
	}
	if validatesInput(entity.ConnectorRunSourceWorkflow) || validatesInput(entity.ConnectorRunSourceTest) {
		t.Fatal("workflow and test runs keep the lenient path")
	}
}
