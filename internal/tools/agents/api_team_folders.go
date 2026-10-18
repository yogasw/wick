package agents

import (
	"context"
	"net/http"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/pkg/tool"
)

// teamFoldersOf is the GET/PUT answer: the caller's folders and
// placements, plus "inherited" — for each agent shared with the caller
// that they have not filed themselves, the name of the folder its OWNER
// keeps it in. The sidebar shows such an agent in a folder of that name
// until the caller moves it.
func teamFoldersOf(ctx context.Context, uid string, f team.Folders) (map[string]any, error) {
	shared, _, err := sharedAgentsFor(ctx, uid)
	if err != nil {
		return nil, err
	}
	inherited := map[string]string{}
	owners := map[string]team.Folders{}
	for _, p := range shared {
		if _, mine := f.Place[p.ID]; mine {
			continue
		}
		of, ok := owners[p.OwnerUserID]
		if !ok {
			if of, err = globalTeam.TeamFolders(ctx, p.OwnerUserID); err != nil {
				return nil, err
			}
			owners[p.OwnerUserID] = of
		}
		if n := of.NameOf(p.ID); n != "" {
			inherited[p.ID] = n
		}
	}
	return map[string]any{"folders": f.Folders, "place": f.Place, "inherited": inherited}, nil
}

// apiTeamFoldersGet handles GET /api/team/folders: the caller's own
// Team-sidebar folders.
func apiTeamFoldersGet(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	uid := actorID(c)
	f, err := globalTeam.TeamFolders(c.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out, err := teamFoldersOf(c.Context(), uid, f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

// apiTeamFoldersSave handles PUT /api/team/folders: replaces the caller's
// whole folders row. An invalid folder list is a 400 and nothing is
// saved; a placement of an agent the caller cannot see (neither theirs
// nor shared with them) is dropped without a word.
func apiTeamFoldersSave(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	var f team.Folders
	if err := c.BindJSON(&f); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := f.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, uid := c.Context(), actorID(c)
	visible, err := visibleTeamAgents(ctx, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for a := range f.Place {
		if !visible[a] {
			delete(f.Place, a)
		}
	}
	// The PUT replaces the whole row, but still under the user's folders
	// lock, so it cannot interleave with a Captain agents.folders write.
	saved, err := globalTeam.UpdateTeamFolders(ctx, uid, func(cur *team.Folders) error {
		*cur = f
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	publishFoldersChanged(uid)
	out, err := teamFoldersOf(ctx, uid, saved)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

// visibleTeamAgents is the ids of every agent uid's sidebar shows: their
// own and those shared with them.
func visibleTeamAgents(ctx context.Context, uid string) (map[string]bool, error) {
	own, err := globalTeam.List(ctx, uid)
	if err != nil {
		return nil, err
	}
	shared, _, err := sharedAgentsFor(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, p := range own {
		out[p.ID] = true
	}
	for _, p := range shared {
		out[p.ID] = true
	}
	return out, nil
}
