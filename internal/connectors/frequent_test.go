package connectors

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

func freqModule() connector.Module {
	return connector.Module{Operations: []connector.Category{{Title: "", Ops: []connector.Operation{
		{Key: "list_items", Input: []entity.Config{{Key: "q", Type: "text"}}},
		{Key: "get_item", Input: []entity.Config{{Key: "id", Type: "text", Required: true}, {Key: "mode", Type: "dropdown", Options: "a|b"}}},
		{Key: "delete_item", Destructive: true},
		{Key: "rare_item"},
		{Key: "helper", ConfigOnly: true},
	}}}}
}

func TestRankUsageWeighsOwnCalls(t *testing.T) {
	own := []usageRow{{ConnectorID: "c1", OperationKey: "get_item", Calls: 3}}
	all := []usageRow{
		{ConnectorID: "c1", OperationKey: "list_items", Calls: 6},
		{ConnectorID: "c1", OperationKey: "get_item", Calls: 3},
	}
	r := rankUsage(own, all)
	if r[0].OperationKey != "get_item" {
		t.Fatalf("own usage should rank first: %+v", r)
	}
	if r[0].Calls != 3 {
		t.Fatalf("calls counts the agent's own: %d", r[0].Calls)
	}
}

func TestPickFrequentRules(t *testing.T) {
	ranking := []OpUsage{
		{ConnectorID: "c1", OperationKey: "delete_item", Score: 100},
		{ConnectorID: "c1", OperationKey: "list_items", Score: 50},
		{ConnectorID: "c1", OperationKey: "get_item", Score: 40},
		{ConnectorID: "c1", OperationKey: "gone_op", Score: 30},
		{ConnectorID: "c2", OperationKey: "get_item", Score: 20},
	}
	prefs := map[string]entity.ConnectorOpPref{
		"c1/rare_item":  {ConnectorID: "c1", OperationKey: "rare_item", Pinned: true, Note: "pinned note"},
		"c1/list_items": {ConnectorID: "c1", OperationKey: "list_items", Muted: true},
	}
	got := PickFrequent(freqModule(), "c1", ranking, prefs, nil, 5)
	var keys []string
	for _, f := range got {
		keys = append(keys, f.Op.Key)
	}
	// pinned first; destructive, muted, missing and other-connector ops left out.
	if len(keys) != 2 || keys[0] != "rare_item" || keys[1] != "get_item" {
		t.Fatalf("picked %v", keys)
	}
	if got[0].Note != "pinned note" || !got[0].Pinned {
		t.Fatalf("pin carries its note: %+v", got[0])
	}
	if n := len(PickFrequent(freqModule(), "c1", ranking, prefs, nil, 1)); n != 1 {
		t.Fatalf("limit ignored: %d", n)
	}
	off := PickFrequent(freqModule(), "c1", ranking, nil, func(op string) bool { return op != "get_item" }, 5)
	for _, f := range off {
		if f.Op.Key == "get_item" {
			t.Fatal("disabled op must not be picked")
		}
	}
}

func TestSignature(t *testing.T) {
	op := freqModule().Operations[0].Ops[1]
	if got := Signature(op); got != "get_item(id: string, mode?: a|b)" {
		t.Fatalf("signature = %s", got)
	}
}
