package agents

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
)

// folderView is agents.folders list's answer.
type folderView struct {
	Folders []captainFolder
	Loose   []string
}

func asFolderView(t *testing.T, out any, err error) folderView {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	return folderView{Folders: m["folders"].([]captainFolder), Loose: m["loose"].([]string)}
}

func TestCaptainFoldersOps(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	ops := CaptainOps{}
	do := func(in teamagents.FoldersInput) folderView {
		t.Helper()
		out, err := ops.Folders(ctx, "s-captain", in)
		return asFolderView(t, out, err)
	}

	v := do(teamagents.FoldersInput{Action: "list"})
	if len(v.Folders) != 0 || strings.Join(v.Loose, ",") != "@captain,@worker" {
		t.Fatalf("empty list = %+v", v)
	}
	v = do(teamagents.FoldersInput{Action: "move", Agent: "@worker", Folder: "Ops"})
	if len(v.Folders) != 1 || v.Folders[0].Name != "Ops" || strings.Join(v.Folders[0].Agents, ",") != "@worker" {
		t.Fatalf("move = %+v", v)
	}
	v = do(teamagents.FoldersInput{Action: "move", Agent: "captain", Folder: "Leads"})
	if strings.Join(v.Folders[1].Agents, ",") != "@captain" || len(v.Loose) != 0 {
		t.Fatalf("Captain files itself = %+v", v)
	}
	v = do(teamagents.FoldersInput{Action: "order", Order: []string{"leads"}})
	if v.Folders[0].Name != "Leads" || v.Folders[1].Name != "Ops" {
		t.Fatalf("order = %+v", v)
	}
	v = do(teamagents.FoldersInput{Action: "rename", Folder: "ops", NewName: "Operations"})
	if v.Folders[1].Name != "Operations" || strings.Join(v.Folders[1].Agents, ",") != "@worker" {
		t.Fatalf("rename = %+v", v)
	}
	v = do(teamagents.FoldersInput{Action: "move", Agent: w.worker.ID, Folder: ""})
	if len(v.Folders[1].Agents) != 0 || strings.Join(v.Loose, ",") != "@worker" {
		t.Fatalf("move out = %+v", v)
	}
	_ = do(teamagents.FoldersInput{Action: "move", Agent: "@worker", Folder: "Operations"})
	v = do(teamagents.FoldersInput{Action: "delete", Folder: "Operations"})
	if len(v.Folders) != 1 || strings.Join(v.Loose, ",") != "@worker" {
		t.Fatalf("delete = %+v", v)
	}
	// It is the OWNER's row the op wrote.
	if f, _ := globalTeam.TeamFolders(ctx, "u1"); f.NameOf(w.captain.ID) != "Leads" {
		t.Fatalf("owner row = %+v", f)
	}

	if _, err := ops.Folders(ctx, "s-captain", teamagents.FoldersInput{Action: "move", Agent: "@other", Folder: "Ops"}); err == nil {
		t.Error("moving a stranger's agent must fail")
	}
	if _, err := ops.Folders(ctx, "s-captain", teamagents.FoldersInput{Action: "move", Agent: w.stranger.ID, Folder: "Ops"}); err == nil {
		t.Error("moving a stranger's agent by id must fail")
	}
	if _, err := ops.Folders(ctx, "s-captain", teamagents.FoldersInput{Action: "rename", Folder: "nope", NewName: "x"}); !errors.Is(err, team.ErrFolderAbsent) {
		t.Errorf("rename missing err = %v", err)
	}
	if _, err := ops.Folders(ctx, "s-captain", teamagents.FoldersInput{Action: "shuffle"}); err == nil {
		t.Error("unknown action must fail")
	}
	if _, err := ops.Folders(ctx, "s-worker", teamagents.FoldersInput{Action: "list"}); !errors.Is(err, team.ErrNotManager) {
		t.Errorf("non-manager err = %v", err)
	}
}

func TestCaptainCreateFilesIntoFolder(t *testing.T) {
	withCaptainWorld(t)
	ctx := context.Background()
	out, err := CaptainOps{}.Create(ctx, "s-captain", teamagents.CreateInput{Name: "Rekap", Folder: "Reports"})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	f, _ := globalTeam.TeamFolders(ctx, "u1")
	if res["folder"] != "Reports" || f.NameOf(res["id"].(string)) != "Reports" {
		t.Fatalf("create with folder: %v %+v", res, f)
	}
}

func TestCaptainFoldersRefusedWhenOff(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	w.captain.CaptainPolicy = team.EncodeCaptainPolicy(team.CaptainPolicy{NoFolders: true})
	if err := globalTeam.Update(ctx, &w.captain); err != nil {
		t.Fatal(err)
	}
	if _, err := (CaptainOps{}).Folders(ctx, "s-captain", teamagents.FoldersInput{Action: "list"}); !errors.Is(err, team.ErrNoFolders) {
		t.Fatalf("err = %v", err)
	}
	if _, err := (CaptainOps{}).Create(ctx, "s-captain", teamagents.CreateInput{Name: "Rekap", Folder: "Reports"}); !errors.Is(err, team.ErrNoFolders) {
		t.Fatalf("create with folder err = %v", err)
	}
	if _, err := globalTeam.GetByHandle(ctx, "u1", "rekap"); err == nil {
		t.Error("a refused create must not make the agent")
	}
	// Without a folder, create still works.
	if _, err := (CaptainOps{}).Create(ctx, "s-captain", teamagents.CreateInput{Name: "Rekap"}); err != nil {
		t.Fatalf("plain create: %v", err)
	}
	if !strings.Contains(team.ErrNoFolders.Error(), "Settings › Captain") {
		t.Error("refusal must point at Settings › Captain")
	}
}
