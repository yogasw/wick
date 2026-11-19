package handlers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

func TestCapabilitySummaryDropsGroupWords(t *testing.T) {
	cat := connector.Category{Title: "Widget Things", Ops: []connector.Operation{
		{Key: "list_widget_things_tool"},
		{Key: "get_widget_thing_logs_tool"},
		{Key: "delete_widget_thing_tool", Destructive: true},
		{Key: "off_op"},
	}}
	got := capabilitySummary(cat, func(k string) bool { return k != "off_op" })
	if got != "list · get-logs · delete⚠" {
		t.Fatalf("summary = %q", got)
	}
}

func bigModule(n int) connector.Module {
	var a, b []connector.Operation
	for i := 0; i < n; i++ {
		op := connector.Operation{Key: fmt.Sprintf("get_thing_%d", i), Name: "Get", Input: []entity.Config{{Key: "id", Required: true}}}
		if i%2 == 0 {
			a = append(a, op)
		} else {
			b = append(b, op)
		}
	}
	return connector.Module{Operations: []connector.Category{{Title: "Alpha", Ops: a}, {Title: "Beta", Ops: b}}}
}

func TestFlatThresholdListsSmallConnectorsWhole(t *testing.T) {
	all := func(string) bool { return true }
	none := func(string) string { return "" }
	tid := func(k string) string { return FormatToolID("c1", k) }
	small, err := buildConnectorDetail(bigModule(FlatOpThreshold), "c1", "L", "", "", tid, all, none)
	if err != nil {
		t.Fatal(err)
	}
	if len(small.Categories) != 0 || len(small.Tools) != FlatOpThreshold {
		t.Fatalf("small connector must list ops flat: %d cats, %d tools", len(small.Categories), len(small.Tools))
	}
	if small.Tools[0].Category == "" {
		t.Fatal("flat listing keeps each op's group name")
	}
	big, err := buildConnectorDetail(bigModule(FlatOpThreshold+5), "c1", "L", "", "", tid, all, none)
	if err != nil {
		t.Fatal(err)
	}
	if len(big.Categories) != 2 || big.Categories[0].Summary == "" {
		t.Fatalf("big connector must group with summaries: %+v", big.Categories)
	}
}

func TestInputErrorBodyCarriesSchema(t *testing.T) {
	e := &connectors.InputError{Operation: "get_x", Problems: []string{"id is required"}, Unknown: []string{"xid"},
		Inputs: []entity.Config{{Key: "id", Required: true}}}
	body := inputErrorBody("conn:c1/get_x", e)
	for _, want := range []string{`"input_schema"`, `"unknown_params"`, `id is required`, `Nothing ran`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body misses %s: %s", want, body)
		}
	}
}
