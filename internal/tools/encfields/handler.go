// Package encfields backs /tools/encfields — the in-app form for
// minting and reversing wick_enc_ tokens. The MCP meta-tools
// wick_encrypt / wick_decrypt deliberately redirect here rather than
// running the cipher inline, so plaintext never lands in an LLM
// context window. Per-user keys (HKDF salted with user.ID) mean only
// the user who issued a token can decrypt it; admins cannot peek at
// other users' values.
//
// Submission flow is JSON-only — the form posts via fetch() and
// renders the result inline, so the browser never reloads. Back from
// /tools/encfields therefore lands on whatever page the user came
// from (typically the home grid), not on a stale form state.
package encfields

import (
	"net/http"

	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// service is the cipher this tool calls into. nil at process start;
// server.go sets it after enc.New, before any request can land. The
// handlers refuse to operate when it's nil so a misconfigured boot
// surfaces an explicit error rather than a silent passthrough.
var service *enc.Service

// SetService wires in the enc.Service the handlers will use. Call
// once from server.go after enc.New.
func SetService(s *enc.Service) { service = s }

// Register mounts the four routes under /tools/encfields. GET serves
// the page shell; POST is a JSON-only API consumed by encfields.js.
// Paths are relative — the framework prefixes /tools/encfields.
func Register(r tool.Router) {
	r.GET("/", encryptPage)
	r.POST("/", encryptAPI)
	r.GET("/decrypt", decryptPage)
	r.POST("/decrypt", decryptAPI)
	r.Static("/static/", StaticFS)
}

// ── encrypt ───────────────────────────────────────────────────────

func encryptPage(c *tool.Ctx) {
	c.HTML(EncryptPage(c.Base()))
}

type encryptRequest struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

type encryptResponse struct {
	Token  string `json:"token,omitempty"`
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

func encryptAPI(c *tool.Ctx) {
	user := login.GetUser(c.R.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, encryptResponse{Error: "Not signed in."})
		return
	}
	var req encryptRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, encryptResponse{Error: "Invalid request body."})
		return
	}
	switch {
	case service == nil:
		c.JSON(http.StatusServiceUnavailable, encryptResponse{Error: "Encryption service is not configured."})
	case service.Disabled():
		c.JSON(http.StatusServiceUnavailable, encryptResponse{Error: "Encryption is disabled (WICK_ENC_DISABLE)."})
	case req.Value == "":
		c.JSON(http.StatusBadRequest, encryptResponse{Error: "Value is required."})
	default:
		token, err := service.EncryptValue(req.Value, user.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, encryptResponse{Error: "Encrypt failed: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, encryptResponse{Token: token, Source: req.Source})
	}
}

// ── decrypt ───────────────────────────────────────────────────────

func decryptPage(c *tool.Ctx) {
	c.HTML(DecryptPage(c.Base()))
}

type decryptRequest struct {
	Token string `json:"token"`
}

type decryptResponse struct {
	Plain string `json:"plain,omitempty"`
	Error string `json:"error,omitempty"`
}

func decryptAPI(c *tool.Ctx) {
	user := login.GetUser(c.R.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, decryptResponse{Error: "Not signed in."})
		return
	}
	var req decryptRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, decryptResponse{Error: "Invalid request body."})
		return
	}
	switch {
	case service == nil:
		c.JSON(http.StatusServiceUnavailable, decryptResponse{Error: "Encryption service is not configured."})
	case service.Disabled():
		c.JSON(http.StatusServiceUnavailable, decryptResponse{Error: "Encryption is disabled (WICK_ENC_DISABLE)."})
	case req.Token == "":
		c.JSON(http.StatusBadRequest, decryptResponse{Error: "Token is required."})
	case !enc.IsToken(req.Token):
		c.JSON(http.StatusBadRequest, decryptResponse{Error: "Not a wick_enc_ token."})
	default:
		plain, err := service.DecryptValue(req.Token, user.ID)
		if err != nil {
			c.JSON(http.StatusBadRequest, decryptResponse{Error: "Decrypt failed. The token may have been issued by a different user, or the master key has been rotated since it was minted."})
			return
		}
		c.JSON(http.StatusOK, decryptResponse{Plain: plain})
	}
}
