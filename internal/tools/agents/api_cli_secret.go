package agents

import (
	"context"
	"errors"
	"net/http"

	"github.com/yogasw/wick/internal/credreq"
	"github.com/yogasw/wick/pkg/tool"
)

// sessionSecrets resolves wick_cred_ tokens for `support-tools agent
// secret`. Wired by the server; nil answers that the feature is off.
var sessionSecrets interface {
	Resolve(ctx context.Context, sessionID, token string) (string, error)
}

// SetSessionSecrets wires the credential-request service.
func SetSessionSecrets(s *credreq.Service) { sessionSecrets = s }

// apiCLISecret resolves one credential token for a script running in the
// token's session. The session comes from the CLI token, never the body, so
// a token can only ever read its own session's values. The value goes back
// to the script only; nothing is logged.
func apiCLISecret(c *tool.Ctx) {
	g, ok := cliGrant(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "no token"})
		return
	}
	if sessionSecrets == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "session secrets are not available on this wick"})
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := c.BindJSON(&body); err != nil || body.Token == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "token is required"})
		return
	}
	val, err := sessionSecrets.Resolve(c.Context(), g.SessionID, body.Token)
	switch {
	case errors.Is(err, credreq.ErrNotFound), errors.Is(err, credreq.ErrWrongScope):
		c.JSON(http.StatusNotFound, map[string]string{"error": "no such secret in this session"})
		return
	case errors.Is(err, credreq.ErrExpired):
		c.JSON(http.StatusGone, map[string]string{"error": err.Error()})
		return
	case err != nil:
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"value": val})
}
