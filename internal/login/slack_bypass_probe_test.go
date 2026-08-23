package login

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// TestProbe_AgentsToolIsOpenToAnyApprovedUser probes the assumption that a
// Slack-registered user still needs a tag before it can reach the agents tool.
//
// The agents tool ships VisibilityPrivate with DefaultTags{tags.AI}, which
// reads like a gate. But tags.AI is declared IsGroup, NOT IsFilter, and
// GetToolFilterTagIDs only counts is_filter rows. So the tool has ZERO filter
// tags, and CanAccessTool's "untagged = open" branch lets every approved user
// through with no tag at all.
func TestProbe_AgentsToolIsOpenToAnyApprovedUser(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	ctx := context.Background()

	// The AI tag exactly as tags.AI declares it: a grouping label.
	aiTag := entity.Tag{Name: "AI", IsGroup: true}
	if err := db.Create(&aiTag).Error; err != nil {
		t.Fatalf("create AI tag: %v", err)
	}
	if err := db.Create(&entity.ToolTag{ToolPath: "/tools/agents", TagID: aiTag.ID}).Error; err != nil {
		t.Fatalf("link tag: %v", err)
	}

	if ids := svc.repo.GetToolFilterTagIDs(ctx, "/tools/agents"); len(ids) != 0 {
		t.Fatalf("filter tags = %v; probe assumption wrong", ids)
	}

	approvedNoTags := &entity.User{ID: "u-app", Email: "a@example.com",
		Name: "Approved", Role: entity.RoleUser, Approved: true}
	if svc.CanAccessTool(ctx, approvedNoTags, "/tools/agents", entity.VisibilityPrivate) {
		t.Log("CONFIRMED: an approved user with NO tags can reach /tools/agents — " +
			"tags.AI is IsGroup, so the tool has no filter tags and 'untagged = open' applies")
	} else {
		t.Fatal("approved user was blocked; the agents tool does carry a filter tag")
	}

	// Approval is the ONLY thing standing in the way.
	pending := &entity.User{ID: "u-pend", Email: "p@example.com",
		Name: "Pending", Role: entity.RoleUser, Approved: false}
	if svc.CanAccessTool(ctx, pending, "/tools/agents", entity.VisibilityPrivate) {
		t.Fatal("UNAPPROVED user reached the agents tool — approval gate is broken")
	}
	t.Log("CONFIRMED: approval is the only gate; an unapproved user is blocked")
}

// TestProbe_SlackPathNeverConsultsCanAccessTool is the actual finding.
//
// The dashboard route for /tools/agents is wrapped in a CanAccessTool check.
// The Slack channel path is not: it resolves an owner and spawns. So the
// question "can this user reach the agents tool" is asked on the web and never
// asked over Slack.
//
// That means approval — not tags — is the whole boundary for Slack, which is
// exactly why the auto-registered account must arrive unapproved.
func TestProbe_SlackPathNeverConsultsCanAccessTool(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")

	id, err := svc.RegisterChannelUser(context.Background(), "new@example.com", "New", "slack")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	var u entity.User
	if err := db.First(&u, "id = ?", id).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if u.Approved {
		t.Fatal("auto-registered user is approved — with no tag gate on the agents " +
			"tool and no CanAccessTool check on the Slack path, that is unrestricted access")
	}
	t.Log("CONFIRMED: auto-register yields an unapproved account, which is the " +
		"only thing keeping a Slack sender from reaching the agent")
}

// TestGateOrder_ApprovalThenAgentAccess pins the priority the Slack path
// relies on: approval first, then access to the agents tool.
//
// Both are checked through CanAccessTool against "/tools/agents", the same
// question the dashboard asks, so a user blocked on the web is blocked in
// Slack. The order matters for the message the sender gets: unapproved is
// "wait for an admin", tag-blocked is "ask for access".
func TestGateOrder_ApprovalThenAgentAccess(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	ctx := context.Background()

	// A real filter tag on the agents tool, unlike tags.AI which is IsGroup.
	gate := entity.Tag{Name: "agent-users", IsFilter: true}
	if err := db.Create(&gate).Error; err != nil {
		t.Fatalf("create tag: %v", err)
	}
	if err := db.Create(&entity.ToolTag{ToolPath: "/tools/agents", TagID: gate.ID}).Error; err != nil {
		t.Fatalf("link tag: %v", err)
	}

	cases := []struct {
		name     string
		approved bool
		withTag  bool
		want     bool
	}{
		{"unapproved, no tag — blocked on approval", false, false, false},
		{"unapproved, tagged — approval still wins", false, true, false},
		{"approved, no tag — blocked on tag", true, false, false},
		{"approved and tagged — allowed", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &entity.User{ID: "u-" + tc.name, Email: tc.name + "@example.com",
				Name: "T", Role: entity.RoleUser, Approved: tc.approved}
			if err := db.Create(u).Error; err != nil {
				t.Fatalf("seed: %v", err)
			}
			var tagIDs []string
			if tc.withTag {
				if err := db.Create(&entity.UserTag{UserID: u.ID, TagID: gate.ID}).Error; err != nil {
					t.Fatalf("link user tag: %v", err)
				}
				tagIDs = []string{gate.ID}
			}
			c := WithUser(ctx, u, tagIDs)
			if got := svc.CanAccessTool(c, u, "/tools/agents", entity.VisibilityPrivate); got != tc.want {
				t.Fatalf("CanAccessTool = %v, want %v", got, tc.want)
			}
		})
	}
}
