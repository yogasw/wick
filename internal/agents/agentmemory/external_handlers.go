package agentmemory

// The panel's half of external access: read the state, flip the switch, mint
// and revoke the token. All four are MANAGING endpoints (PLAN §23.2) — they
// decide whether client work captured in agent sessions can be read from off
// this machine, which is the most consequential switch in the feature.

import (
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/tool"
)

// registerExternalRoutes wires the external-access controls for one backend.
// Called from RegisterRoutes; split out so the gate and the proxy live in one
// pair of files rather than being spread through the main handler set.
func registerExternalRoutes(r tool.Router, be *Backend, p string) {
	r.GET(p+"/external", manage(be, externalStateHandler))
	r.POST(p+"/external", manage(be, setExternalHandler))
	r.POST(p+"/external/token", manage(be, mintExternalTokenHandler))
	r.POST(p+"/external/revoke", manage(be, revokeExternalHandler))
}

func externalStateHandler(be *Backend, c *tool.Ctx) {
	c.JSON(http.StatusOK, map[string]any{"external": externalStateFor(be, c)})
}

// setExternalHandler flips the switch. Turning it ON without a token is
// refused rather than allowed-and-useless: an enabled route with no token
// answers 403 to everything, which looks like a broken feature instead of the
// deliberate fail-closed it is.
func setExternalHandler(be *Backend, c *tool.Ctx) {
	on := formBool(c, "enabled", false)
	if on && strings.TrimSpace(store.ExternalToken(be.Desc.ID)) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "create an access token first — external access with no token refuses every request",
		})
		return
	}
	if err := store.SetExternalEnabled(c.Context(), be.Desc.ID, on); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"external": externalStateFor(be, c)})
}

// mintExternalTokenHandler issues a new token and returns it ONCE. It is
// never readable again — the state payload only ever says whether one exists.
// Minting replaces any previous token, so this is also how a leaked one is
// rotated: the old value stops working the moment this returns.
func mintExternalTokenHandler(be *Backend, c *tool.Ctx) {
	tok, err := newExternalToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not generate a token: " + err.Error()})
		return
	}
	if err := store.SetExternalToken(c.Context(), be.Desc.ID, tok); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		// The only time a token is ever sent to a browser.
		"token":    tok,
		"external": externalStateFor(be, c),
	})
}

// revokeExternalHandler clears the token AND switches external access off.
// Both, because the two states that would remain are worse than either: an
// enabled route with no token is a 403 machine, and a token left behind a
// switched-off route is a credential nobody remembers is still valid.
func revokeExternalHandler(be *Backend, c *tool.Ctx) {
	if err := store.SetExternalToken(c.Context(), be.Desc.ID, ""); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := store.SetExternalEnabled(c.Context(), be.Desc.ID, false); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"external": externalStateFor(be, c)})
}

// externalStateFor assembles the block the Settings tab renders. It reports
// the EXISTENCE of a token, never the token.
func externalStateFor(be *Backend, c *tool.Ctx) ExternalState {
	id := be.Desc.ID
	allowed, rejected, recent := be.Mgr.ExternalSnapshot()
	return ExternalState{
		Enabled:       store.ExternalEnabled(id),
		HasToken:      strings.TrimSpace(store.ExternalToken(id)) != "",
		URL:           externalURL(c, id),
		Paths:         append([]string(nil), externalUpstreamPrefixes...),
		AllowedTotal:  allowed,
		RejectedTotal: rejected,
		Recent:        recent,
	}
}

// externalURL is the address to hand a script. It is built from the request
// rather than from app_url on purpose: the operator is looking at the panel
// through some hostname right now, and that is the one that demonstrably
// reaches this wick.
func externalURL(c *tool.Ctx, id string) string {
	scheme := "http"
	if c.R != nil {
		if c.R.TLS != nil || strings.EqualFold(c.R.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
	}
	host := ""
	if c.R != nil {
		host = c.R.Host
	}
	if host == "" {
		return ExternalMountPath(id)
	}
	return scheme + "://" + host + ExternalMountPath(id)
}
