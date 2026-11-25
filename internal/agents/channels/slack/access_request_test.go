package slack

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/accessreq"
)

func sampleAccessView() accessreq.View {
	approvers := []accessreq.Person{{ID: "u1", Name: "Approver A"}}
	return accessreq.View{
		ID: "11111111-2222", SessionID: "s1", AgentName: "@helper", Reason: "update the <status>", Status: accessreq.StatusPending,
		URL: "https://wick.example.com/tools/agents/access/11111111-2222", Pending: 2,
		Items: []accessreq.ItemView{
			{ID: "i1", Idx: 0, ConnectorKey: "notes", OpKey: "update_page", Kind: accessreq.KindWrite, Target: "page_id=p1",
				Status: accessreq.ItemPending, Approvers: approvers, CanDecide: true, Modes: []string{"once", "session"},
				Params: []accessreq.Param{{Name: "page_id", Value: "p1"}}},
			{ID: "i2", Idx: 1, ConnectorKey: "repo", OpKey: "delete_branch", Kind: accessreq.KindDelete,
				Status: accessreq.ItemPending, Approvers: approvers, LockedCode: accessreq.ReasonApproverAccess,
				LockedReason: "You are an approver, but your own access cannot run delete_branch."},
			{ID: "i3", Idx: 2, ConnectorKey: "chat", OpKey: "post_message", Kind: accessreq.KindSend,
				Status: accessreq.ItemError, Mode: "once", DecidedBy: "Approver A", Error: "channel_not_found"},
		},
		ViewerCanDecide: true,
	}
}

func TestAccessCardBlocksParse(t *testing.T) {
	v := sampleAccessView()
	blocks, err := blocksFromJSON(accessCardBlocks(v))
	if err != nil || len(blocks) == 0 {
		t.Fatalf("card blocks: %v (%d)", err, len(blocks))
	}
	// Encode without HTML escaping, so the text reads as Slack receives it.
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(accessCardBlocks(v))
	s := sb.String()
	for _, want := range []string{"Review & decide", accessReviewAction, accessDenyAction, "channel_not_found", "Approver A", "update the &lt;status&gt;"} {
		if !strings.Contains(s, want) {
			t.Errorf("card misses %q", want)
		}
	}
	if strings.Contains(s, "Open in web") {
		t.Error("the card must not link to a separate page")
	}
	// A decided request has no buttons left.
	v.Pending = 0
	raw, _ := json.Marshal(accessCardBlocks(v))
	if strings.Contains(string(raw), accessReviewAction) {
		t.Error("buttons shown with nothing pending")
	}
}

func TestAccessReviewModalParses(t *testing.T) {
	v := sampleAccessView()
	m := reviewModal(v, "11111111-2222|C1|1.2")
	req, err := toModalRequest(m)
	if err != nil {
		t.Fatalf("modal: %v", err)
	}
	if req.CallbackID != accessDecideCallback || req.PrivateMetadata != "11111111-2222|C1|1.2" {
		t.Fatalf("modal ids = %q %q", req.CallbackID, req.PrivateMetadata)
	}
	raw, _ := json.Marshal(m)
	s := string(raw)
	if !strings.Contains(s, accessItemBlockPfx+"i1") || strings.Contains(s, accessItemBlockPfx+"i2") {
		t.Error("only the decidable item gets a picker")
	}
	if !strings.Contains(s, "your own access cannot run delete_branch") {
		t.Error("locked row lost its reason")
	}
	if !strings.Contains(s, "Save decisions") {
		t.Error("no submit for a viewer who can decide")
	}
}

func TestAccessNoAccessModalHasNoSubmit(t *testing.T) {
	v := sampleAccessView()
	v.ViewerCanDecide = false
	v.ViewerReason = "You do not have access to approve this request. Who can: Approver A."
	v.ViewerReasonCode = accessreq.ReasonNotApprover
	for i := range v.Items {
		v.Items[i].CanDecide = false
	}
	m := reviewModal(v, "x|C1|1.2")
	if _, ok := m["submit"]; ok {
		t.Fatal("no-access modal must not offer submit")
	}
	raw, _ := json.Marshal(m)
	if !strings.Contains(string(raw), "No access") || !strings.Contains(string(raw), "Who can: Approver A") {
		t.Fatalf("no-access modal = %s", raw)
	}
	// The deny button falls back to the same no-access modal.
	if _, ok := denyModal(v, "x|C1|1.2")["submit"]; ok {
		t.Fatal("deny modal offered to a viewer who cannot decide")
	}
}

func TestAccessStateParsing(t *testing.T) {
	values := map[string]any{
		accessItemBlockPfx + "i1": map[string]any{"mode": map[string]any{"selected_option": map[string]any{"value": "session"}}},
		accessNoteBlock:           map[string]any{"text": map[string]any{"value": "  not this one  "}},
	}
	if got := stateSelected(values, accessItemBlockPfx+"i1", "mode"); got != "session" {
		t.Errorf("selected = %q", got)
	}
	if got := stateText(values, accessNoteBlock, "text"); got != "not this one" {
		t.Errorf("note = %q", got)
	}
	if got := stateSelected(values, "missing", "mode"); got != "" {
		t.Errorf("missing = %q", got)
	}
}

func TestAccessModalSenderHeader(t *testing.T) {
	m := withSenderHeader(reviewModal(sampleAccessView(), "x|C1|1.2"), "Helper Bot", "https://example.com/a.png")
	if _, err := toModalRequest(m); err != nil {
		t.Fatalf("modal with header: %v", err)
	}
	blocks := m["blocks"].([]map[string]any)
	raw, _ := json.Marshal(blocks[0])
	if !strings.Contains(string(raw), "Helper Bot") || !strings.Contains(string(raw), "example.com/a.png") {
		t.Fatalf("first block = %s", raw)
	}
}

func TestCancelledCardNamesNobody(t *testing.T) {
	v := accessreq.View{ID: "req-c", Status: accessreq.StatusCancelled, Items: []accessreq.ItemView{{
		ConnectorKey: "acme", OpKey: "publish", Status: accessreq.ItemPending,
		Approvers: []accessreq.Person{{ID: "u1", Name: "Person A"}, {ID: "u2", Name: "Person B"}},
	}}}
	b, _ := json.Marshal(accessCardBlocks(v))
	s := string(b)
	if strings.Contains(s, "Person A") || strings.Contains(s, "who can approve") {
		t.Fatalf("cancelled card still lists approvers: %s", s)
	}
	if !strings.Contains(s, "Not decided") {
		t.Fatalf("cancelled card misses the not-decided line: %s", s)
	}
}

func longAccessView(n int) accessreq.View {
	v := accessreq.View{ID: "33333333-4444", AgentName: "@helper", Reason: "step 2 of the plan", Status: accessreq.StatusPending,
		Key: "step-2", Version: 2, Counts: map[string]int{}, ViewerCanDecide: true}
	for i := 0; i < n; i++ {
		op := "update_field"
		if i%3 == 0 {
			op = "update_form"
		}
		it := accessreq.ItemView{ID: "i" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Idx: i, ConnectorKey: "acme", OpKey: op,
			Kind: accessreq.KindWrite, Target: "field_id=f1", Status: accessreq.ItemPending, CanDecide: true,
			Modes: []string{"once", "hour"}, ParamsJSON: `{"field_id":"f1","value":"` + strings.Repeat("x", 80) + `"}`,
			Approvers: []accessreq.Person{{ID: "u1", Name: "Approver A"}}}
		v.Items = append(v.Items, it)
		v.Counts[it.Status]++
		v.Pending++
	}
	return v
}

func TestLongCardGroupsActionsAndHidesPayload(t *testing.T) {
	v := longAccessView(40)
	v.Origin = map[string]string{"slack_file_permalink": "https://files.example.com/F1"}
	raw := accessCardBlocks(v)
	if _, err := blocksFromJSON(raw); err != nil {
		t.Fatalf("blocks: %v", err)
	}
	if len(raw) > 12 {
		t.Fatalf("a 40-action card has %d blocks, want a short card", len(raw))
	}
	b, _ := json.Marshal(raw)
	s := string(b)
	for _, want := range []string{"acme.update_field", "× 26", "acme.update_form", "× 14", "step-2", "v2", "step-2-v2.json", "right below this card", "Approve all (40)"} {
		if !strings.Contains(s, want) {
			t.Errorf("long card misses %q", want)
		}
	}
	if strings.Contains(s, strings.Repeat("x", 80)) {
		t.Error("long card shows the payload inline")
	}
	if strings.Contains(s, "files.example.com") {
		t.Error("the payload file is linked (it unfurls a second preview)")
	}
}

func TestSmallSinglePayloadStaysInline(t *testing.T) {
	v := longAccessView(1)
	b, _ := json.Marshal(accessCardBlocks(v))
	if !strings.Contains(string(b), strings.Repeat("x", 80)) {
		t.Fatal("a single small payload should stay on the card")
	}
}

func TestReplacedCardLinksNewVersion(t *testing.T) {
	v := longAccessView(3)
	v.Status = accessreq.StatusSuperseded
	v.Origin = map[string]string{"slack_channel": "C01TESTCHAN", "slack_thread": "1.0"}
	v.Replacement = &accessreq.Ref{ID: "55555555", Key: "step-2", Version: 3,
		Origin: map[string]string{"slack_channel": "C01TESTCHAN", "slack_thread": "1.0", "slack_permalink": "https://x.slack.com/archives/C01TESTCHAN/p1"}}
	b, _ := json.Marshal(accessCardBlocks(v))
	s := string(b)
	// Same thread: named, never linked (a permalink unfurls a copy of the card).
	if !strings.Contains(s, "replaced") || !strings.Contains(s, "step-2 v3") || !strings.Contains(s, "below in this thread") || strings.Contains(s, "x.slack.com") {
		t.Fatalf("replaced card = %s", s)
	}
	// Another thread: linked.
	v.Replacement.Origin["slack_thread"] = "9.9"
	b, _ = json.Marshal(accessCardBlocks(v))
	if !strings.Contains(string(b), "x.slack.com/archives/C01TESTCHAN/p1|step-2 v3") {
		t.Fatalf("card in another thread is not linked: %s", b)
	}
	if strings.Contains(s, accessApproveAll) {
		t.Fatal("replaced card still has buttons")
	}
}

func TestAfterAndFailureLines(t *testing.T) {
	v := longAccessView(10)
	v.AfterRef = &accessreq.Ref{ID: "66666666", Key: "step-1", Version: 1}
	v.AfterState = accessreq.AfterWaiting
	b, _ := json.Marshal(accessCardBlocks(v))
	if !strings.Contains(string(b), "Runs after `step-1 v1` (request `66666666`) above in this thread") {
		t.Fatalf("after line missing: %s", b)
	}
	for i := range v.Items {
		switch {
		case i < 3:
			v.Items[i].Status = accessreq.ItemOK
		case i == 3:
			v.Items[i].Status, v.Items[i].Error = accessreq.ItemError, "422 bad field"
		default:
			v.Items[i].Status = accessreq.ItemSkipped
		}
	}
	v.Counts = map[string]int{accessreq.ItemOK: 3, accessreq.ItemError: 1, accessreq.ItemSkipped: 6}
	v.Status, v.Pending = accessreq.StatusDone, 0
	b, _ = json.Marshal(accessCardBlocks(v))
	s := string(b)
	if !strings.Contains(s, "Stopped at action 4") || !strings.Contains(s, "6 action(s) after it were not run") {
		t.Fatalf("failure summary missing: %s", s)
	}
}

func TestBulkReviewModalOneDecision(t *testing.T) {
	v := longAccessView(40)
	m := reviewModal(v, "33333333-4444|C1|1.2")
	if _, err := toModalRequest(m); err != nil {
		t.Fatalf("modal: %v", err)
	}
	blocks := m["blocks"].([]map[string]any)
	if len(blocks) >= 100 {
		t.Fatalf("modal has %d blocks", len(blocks))
	}
	b, _ := json.Marshal(m)
	s := string(b)
	if !strings.Contains(s, accessAllBlock) || strings.Contains(s, accessItemBlockPfx) {
		t.Fatal("a long request gets one decision, not one picker per action")
	}
	if !strings.Contains(s, `"value":"hour"`) || strings.Contains(s, `"value":"session"`) {
		t.Fatal("bulk modes must be the ones every action offers")
	}
	ds := bulkDecisions(v, accessreq.ModeDeny, "not now")
	if len(ds) != 40 || ds[0].Note != "not now" {
		t.Fatalf("bulk decisions = %d", len(ds))
	}
}

func TestPayloadFileIsWholeAndNamedByKey(t *testing.T) {
	v := longAccessView(40)
	if got := payloadFileName(v); got != "step-2-v2.json" {
		t.Fatalf("name = %q", got)
	}
	var doc struct {
		Actions []struct {
			N      int            `json:"n"`
			Params map[string]any `json:"params"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(payloadFile(v), &doc); err != nil || len(doc.Actions) != 40 || doc.Actions[39].Params["field_id"] != "f1" {
		t.Fatalf("payload file: %v %d", err, len(doc.Actions))
	}
}

func TestTypedApproveMatches(t *testing.T) {
	for _, s := range []string{"approve", "Approved ✅", "<@U01ABCDEF> approve", "ok approve", "setuju", "acc"} {
		if !typedApproveRe.MatchString(s) {
			t.Errorf("%q should match", s)
		}
	}
	for _, s := range []string{"approve the second card only after the fix", "accessible?", "please review"} {
		if typedApproveRe.MatchString(s) {
			t.Errorf("%q should not match", s)
		}
	}
}
