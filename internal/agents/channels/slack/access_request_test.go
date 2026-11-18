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
	for _, want := range []string{"Review & decide", accessReviewAction, accessDenyAction, "Open in web", "channel_not_found", "Approver A", "update the &lt;status&gt;"} {
		if !strings.Contains(s, want) {
			t.Errorf("card misses %q", want)
		}
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
