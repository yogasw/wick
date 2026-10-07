package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These tests pin the api/v3 payload that update_page_properties sends.
//
// Why they exist: on 2026-09-28 update_page_properties started answering
// `notion 400: Something went wrong.` for EVERY row and EVERY property type,
// while set_title, create_page (with the same properties), append_content and
// every read op on the same connector kept working. That rules out the cookie,
// the endpoint and the caller's params, and leaves the operation this op builds.
// The tests below lock that payload down so a future change — or a fix — is
// visible without a live Notion call.

// A property write must match the shape Notion's own web client sends — the
// high-level updateBlockPropertyValue wrapping the old primitive set. This was
// captured off the wire from a real cell edit on 2026-09-28; the plain set that
// used to live here is what earns the 400 "must use high-level property
// operations". Pin it field by field: every one of them was in the capture, and
// dropping any is a silent regression to the broken shape.
func TestPropOps_MatchesCapturedWebClientShape(t *testing.T) {
	const (
		pageID  = "3e41f07f-4ae0-81eb-938d-e5dca911b4ad"
		spaceID = "827e9fd8-c1e6-4684-b62d-274773e6cb75"
	)

	got := propOps(pageID, spaceID, []propSet{{ID: "{L~P", Value: jsonInline("To Verify")}})
	if len(got) != 1 {
		t.Fatalf("propOps returned %d ops, want 1", len(got))
	}
	o := got[0]

	if o["command"] != "updateBlockPropertyValue" {
		t.Errorf("command = %v, want updateBlockPropertyValue", o["command"])
	}
	wantPtr := map[string]any{"table": "block", "id": pageID, "spaceId": spaceID}
	if !reflect.DeepEqual(o["pointer"], wantPtr) {
		t.Errorf("pointer = %#v, want %#v", o["pointer"], wantPtr)
	}
	if !reflect.DeepEqual(o["path"], []any{"properties", "{L~P"}) {
		t.Errorf("path = %#v", o["path"])
	}
	// The primitive is not gone, it is nested: args.primitiveOp = the old op.
	args, _ := o["args"].(map[string]any)
	prim, _ := args["primitiveOp"].(map[string]any)
	if prim == nil || prim["command"] != "set" {
		t.Fatalf("args.primitiveOp missing or not a set: %#v", o["args"])
	}
	if b, err := json.Marshal(prim["args"]); err != nil || string(b) != `[["To Verify"]]` {
		t.Errorf("primitiveOp.args = %s (err %v), want [[\"To Verify\"]]", b, err)
	}
	// The CRDT slots. Empty expectations = last write wins; the row itself is
	// the pointer the server is told to invalidate.
	if _, ok := o["blockPropertyValueExpectedVersions"]; !ok {
		t.Error("blockPropertyValueExpectedVersions is absent — Notion rejects the op without it")
	}
	add, _ := o["additionalUpdatedPointers"].([]any)
	if len(add) != 1 || !reflect.DeepEqual(add[0], wantPtr) {
		t.Errorf("additionalUpdatedPointers = %#v, want [the row's own pointer]", o["additionalUpdatedPointers"])
	}
}

// The same thing again, but against the RECORDING instead of against what I
// believed the recording said. testdata/captured-property-edit.json is the
// operation Notion's own web client sent for one State edit on 2026-09-28,
// saved byte for byte off the wire. Rebuilding it from propOps must reproduce
// it exactly — same keys, same nesting, same empty CRDT slot. A hand-typed
// expectation can drift from the capture without anyone noticing; this cannot.
func TestPropOps_EqualsCapturedFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "captured-property-edit.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}

	// Same inputs the captured edit had: the ticket row, its space, the State
	// property id, and the option that was picked.
	ptr, _ := want["pointer"].(map[string]any)
	path, _ := want["path"].([]any)
	propID, _ := path[1].(string)
	got := propOps(ptr["id"].(string), ptr["spaceId"].(string),
		[]propSet{{ID: propID, Value: jsonInline("To Verify")}})[0]

	// Compare as JSON so Go's map[string]any and the fixture's types meet on
	// the same ground — the wire format is what has to match, not the Go value.
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var gotAny map[string]any
	if err := json.Unmarshal(gotJSON, &gotAny); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(gotAny, want) {
		g, _ := json.MarshalIndent(gotAny, "", " ")
		w, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("operation differs from what Notion's client sends:\n got = %s\nwant = %s", g, w)
	}
}

// The pointer id must be the DASHED uuid. The private API rejects a bare 32-char
// id here, and updatePageProps passes the caller's id straight into op() — so if
// the service layer ever stops normalising, this is where it shows up.
func TestPropOps_PointerIDIsDashed(t *testing.T) {
	const spaceID = "11111111-2222-3333-4444-555555555555"
	dashed := normalizeID("3e41f07f4ae081eb938de5dca911b4ad")

	ops := propOps(dashed, spaceID, []propSet{{ID: "abc", Value: jsonInline("x")}})
	ptr, _ := ops[0]["pointer"].(map[string]any)
	if ptr["id"] != "3e41f07f-4ae0-81eb-938d-e5dca911b4ad" {
		t.Errorf("pointer id = %v, want the dashed uuid", ptr["id"])
	}
	if ptr["spaceId"] != spaceID {
		t.Errorf("pointer spaceId = %v, want %v", ptr["spaceId"], spaceID)
	}
}

// EVERY property type we accept serialises to the same outer shape as the title
// write: a [[...]] inline array (a date is one too — a "‣" mention segment).
// This is the VALUE, which was never the problem: during the 2026-09-28 outage
// status, select and date all produced a well-formed value and all three were
// still rejected. The fix was the operation wrapping it (propOp), not this — so
// the value shape stays pinned here, separately, and a change to one does not
// quietly hide a change to the other.
func TestFormatProperty_AllTypesSerialiseAsInlineArray(t *testing.T) {
	for _, c := range []struct{ typ, in string }{
		{"status", "To Verify"},
		{"select", "Backend"},
		{"multi_select", "a,b"},
		{"text", "hello"},
		{"checkbox", "true"},
		{"date", "2026-09-28 15:26"},
		{"relation", "3e41f07f-4ae0-81eb-938d-e5dca911b4ad"},
	} {
		v, ok := formatProperty(c.typ, c.in)
		if !ok {
			t.Errorf("%s: formatProperty reported not settable", c.typ)
			continue
		}
		var arr [][]any
		if err := json.Unmarshal(v, &arr); err != nil {
			t.Errorf("%s: value %s is not a [[...]] inline array: %v", c.typ, v, err)
			continue
		}
		if len(arr) == 0 {
			t.Errorf("%s: value %s has no segments", c.typ, v)
		}
	}
}

// Read-only types must never reach propOps — they would be rejected server-side
// and would make a whole transaction fail even when the other cells are valid.
func TestResolveProps_SkipsReadOnlyAndUnknown(t *testing.T) {
	nameToID := map[string]string{"State": "s1", "Spent Time (Hour)": "r1"}
	idToType := map[string]string{"s1": "status", "r1": "rollup"}

	sets, skipped := resolveProps(map[string]string{
		"State":             "To Verify",
		"Spent Time (Hour)": "5",
		"No Such Prop":      "x",
	}, nameToID, idToType)

	if len(sets) != 1 || sets[0].ID != "s1" {
		t.Fatalf("sets = %+v, want only the status property", sets)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped = %v, want the rollup and the unknown name", skipped)
	}
}

// A relation column is flagged so createPage can write it in a second
// transaction: Notion 400s ("association_relation") when the relation is set in
// the same transaction that creates the row.
func TestResolveProps_FlagsRelationColumns(t *testing.T) {
	nameToID := map[string]string{"Ticket": "t1", "Activity": "a1"}
	idToType := map[string]string{"t1": "relation", "a1": "select"}

	sets, _ := resolveProps(map[string]string{
		"Ticket":   "3ed1f07f-4ae0-81c4-afb3-dcedee41dc60",
		"Activity": "Debug",
	}, nameToID, idToType)

	byID := map[string]propSet{}
	for _, s := range sets {
		byID[s.ID] = s
	}
	if !byID["t1"].Relation {
		t.Errorf("relation column not flagged: %+v", byID["t1"])
	}
	if byID["a1"].Relation {
		t.Errorf("select column wrongly flagged as relation: %+v", byID["a1"])
	}
}

// privateError must surface Notion's debugMessage. The private API answers every
// validation failure with a generic message ("Something went wrong. (400)") and
// puts the real reason in debugMessage; reporting only the generic one turns an
// actionable error into a dead end.
func TestPrivateError_PrefersDebugMessage(t *testing.T) {
	body := []byte(`{"isNotionError":true,"name":"ValidationError",` +
		`"debugMessage":"Unsaved transactions: Block property value updates must use high-level property operations.",` +
		`"message":"Something went wrong. (400)"}`)

	err := privateError(400, body)
	if err == nil {
		t.Fatal("expected an error")
	}
	got := err.Error()
	if !strings.Contains(got, "high-level property operations") {
		t.Errorf("error does not carry debugMessage: %q", got)
	}
	if !strings.Contains(got, "Something went wrong") {
		t.Errorf("error dropped the generic message entirely: %q", got)
	}

	// Without debugMessage the generic message is still reported.
	only := privateError(400, []byte(`{"message":"Something went wrong. (400)"}`))
	if only == nil || !strings.Contains(only.Error(), "Something went wrong") {
		t.Errorf("generic-only body lost its message: %v", only)
	}
}
