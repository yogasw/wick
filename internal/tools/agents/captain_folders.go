package agents

import (
	"context"
	"errors"
	"fmt"

	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
	"github.com/yogasw/wick/internal/entity"
)

// captainFolder is one folder as agents.folders list reports it.
type captainFolder struct {
	Name      string   `json:"name"`
	Collapsed bool     `json:"collapsed,omitempty"`
	Agents    []string `json:"agents"`
}

// foldersAllowed refuses when the owner switched Captain folder
// organizing off on the Captain.
func foldersAllowed(mgr entity.AgentPersona) error {
	if team.DecodeCaptainPolicy(mgr.CaptainPolicy).NoFolders {
		return team.ErrNoFolders
	}
	return nil
}

// Folders implements agents.folders: it reads and writes the OWNER's own
// sidebar folders, silently (no chat event — a folder is only layout).
func (o CaptainOps) Folders(ctx context.Context, sessionID string, in teamagents.FoldersInput) (any, error) {
	mgr, err := o.manager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := foldersAllowed(mgr); err != nil {
		return nil, err
	}
	owner := mgr.OwnerUserID
	f, err := globalTeam.TeamFolders(ctx, owner)
	if err != nil {
		return nil, err
	}
	switch in.Action {
	case "list":
		return o.folderList(ctx, owner, f)
	case "move":
		// The manager may file itself too: a folder is the owner's layout,
		// not the agent's settings, so target's "not yourself" rule is off.
		p := mgr
		if in.Agent != mgr.ID && team.NormalizeHandle(in.Agent) != mgr.Handle {
			if p, err = o.target(ctx, mgr, in.Agent); err != nil {
				return nil, err
			}
		}
		if err := f.Move(p.ID, in.Folder); err != nil {
			return nil, err
		}
	case "rename":
		if err := f.Rename(in.Folder, in.NewName); err != nil {
			return nil, err
		}
	case "delete":
		if err := f.Delete(in.Folder); err != nil {
			return nil, err
		}
	case "order":
		if len(in.Order) == 0 {
			return nil, errors.New("order needs folder names, comma-separated")
		}
		if err := f.Order(in.Order); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown action %q; use list, move, rename, delete or order", in.Action)
	}
	if err := globalTeam.SaveTeamFolders(ctx, owner, f); err != nil {
		return nil, err
	}
	return o.folderList(ctx, owner, f)
}

// folderList is f with agent ids as the owner's @handles: every folder
// in order, then the owner's agents in none. Placements of agents that
// are gone or not the owner's are left out.
func (CaptainOps) folderList(ctx context.Context, owner string, f team.Folders) (any, error) {
	rows, err := globalTeam.List(ctx, owner)
	if err != nil {
		return nil, err
	}
	at := map[string]int{}
	out := make([]captainFolder, 0, len(f.Folders))
	for i, d := range f.Folders {
		at[d.ID] = i
		out = append(out, captainFolder{Name: d.Name, Collapsed: d.Collapsed, Agents: []string{}})
	}
	loose := []string{}
	for _, p := range rows {
		if i, ok := at[f.Place[p.ID]]; ok {
			out[i].Agents = append(out[i].Agents, "@"+p.Handle)
		} else {
			loose = append(loose, "@"+p.Handle)
		}
	}
	return map[string]any{"folders": out, "loose": loose}, nil
}

// fileNewAgent puts agentID in the owner's folder called name (made when
// missing), for agents.create.
func fileNewAgent(ctx context.Context, owner, agentID, name string) error {
	f, err := globalTeam.TeamFolders(ctx, owner)
	if err != nil {
		return err
	}
	if err := f.Move(agentID, name); err != nil {
		return err
	}
	return globalTeam.SaveTeamFolders(ctx, owner, f)
}
