package agents

import "github.com/yogasw/wick/pkg/tool"

// teamLandingRedirect answers whether the bare Agents landing should send
// the caller to Team instead: only with no query at all (a ?project=, the
// account menu's ?view=classic or any other deep link is left alone), only when
// the Team app is on and the caller signed in, and only when they left
// "Open Team when I open Agents" on. A failed settings read keeps the
// landing — never a redirect on a guess.
func teamLandingRedirect(c *tool.Ctx) bool {
	if globalTeam == nil || c.R.URL.RawQuery != "" {
		return false
	}
	if p := c.R.URL.Path; p != c.Base() && p != c.Base()+"/" {
		return false
	}
	uid := actorID(c)
	if uid == "" {
		return false
	}
	st, err := globalTeam.Settings(c.Context(), uid)
	return err == nil && st.OpenTeam()
}
