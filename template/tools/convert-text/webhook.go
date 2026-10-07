package converttext

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/pkg/tool"
)

// signatureHeader carries the hex-encoded HMAC-SHA256 of the raw request
// body, keyed by the instance's configured webhook secret.
const signatureHeader = "X-Hook-Signature"

// maxBatch caps how many conversions one request may ask for. The
// endpoint answers without a login, so an unbounded batch would let any
// caller size the work it makes the server do.
const maxBatch = 50

// convertRequest is the accepted payload: a list of conversions to apply
// in one call.
//
//	{"items":[{"text":"hello","type":"uppercase"},
//	          {"text":"WORLD","type":"lowercase"}]}
type convertRequest struct {
	Items []convertItem `json:"items"`
}

type convertItem struct {
	Text string `json:"text"`
	Type string `json:"type"`
}

// convertResponse mirrors the request one-for-one, so a caller can zip
// results back onto what it sent by index.
type convertResponse struct {
	Results []convertResult `json:"results"`
}

type convertResult struct {
	Type   string `json:"type"`
	Result string `json:"result"`
	// Error is set when the item named a type this tool does not implement.
	// Reported per item rather than failing the batch: one bad type in a
	// list of fifty should not discard the other forty-nine.
	Error string `json:"error,omitempty"`
}

// registerWebhook opens the tool's unauthenticated JSON endpoint.
//
// The route is mounted unconditionally — Go's route table is fixed once
// the server boots, so an admin toggling the feature cannot add or remove
// a mount. The toggle is therefore enforced per request inside the
// handler, which is also why it can be flipped without a redeploy.
func registerWebhook(r tool.Router) {
	wh := r.WebhookGroup("/webhook")
	wh.POST("/convert", webhookConvert)
}

// webhookConvert applies a batch of conversions for an external caller.
//
// The order of the checks matters. Nothing upstream authenticated this
// request, so the handler refuses in the cheapest order first: disabled,
// then unconfigured, then unsigned, and only then does it parse the body.
func webhookConvert(c *tool.WebhookCtx) {
	l := log.Ctx(c.Context()).With().Str("tool", c.Meta().Key).Logger()

	if !c.CfgBool("webhook_enabled") {
		// 404 rather than 403: a disabled endpoint should look like it does
		// not exist, so probing cannot enumerate which instances have the
		// feature switched off.
		c.Error(http.StatusNotFound, "not found")
		return
	}

	secret := c.Cfg("webhook_secret")
	if secret == "" {
		// Fail closed. With no key every signature would verify against the
		// empty string, so an enabled-but-unconfigured endpoint must refuse
		// rather than accept everything.
		l.Warn().Msg("webhook enabled without a secret; refusing delivery")
		c.Error(http.StatusServiceUnavailable, "webhook secret not configured")
		return
	}

	// Read the raw bytes before decoding: the signature covers exactly
	// what was sent, and a body can only be consumed once.
	raw, err := c.Body()
	if err != nil {
		c.Error(http.StatusBadRequest, "cannot read body")
		return
	}
	if !validSignature(raw, c.Header(signatureHeader), secret) {
		l.Warn().Msg("webhook delivery refused: bad signature")
		c.Error(http.StatusUnauthorized, "bad signature")
		return
	}

	var req convertRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		c.Error(http.StatusBadRequest, "body is not valid JSON")
		return
	}
	if len(req.Items) == 0 {
		c.Error(http.StatusBadRequest, "items must not be empty")
		return
	}
	if len(req.Items) > maxBatch {
		c.Error(http.StatusRequestEntityTooLarge, "too many items in one request")
		return
	}

	results := make([]convertResult, 0, len(req.Items))
	for _, item := range req.Items {
		ct := ConvertType(item.Type)
		if !supported(ct) {
			results = append(results, convertResult{Type: item.Type, Error: "unknown conversion type"})
			continue
		}
		results = append(results, convertResult{Type: item.Type, Result: Convert(item.Text, ct)})
	}

	l.Info().Int("items", len(results)).Msg("webhook conversion batch applied")
	c.JSON(http.StatusOK, convertResponse{Results: results})
}

// supported reports whether ct is a conversion this tool implements.
//
// Convert returns its input unchanged for an unknown type, which is the
// right behaviour for the form (a stray value should not blank the user's
// text) but the wrong answer for an API: a caller that misspells a type
// would get its input echoed back and read that as success. Checking
// first turns a silent no-op into a per-item error.
func supported(ct ConvertType) bool {
	switch ct {
	case ConvertUppercase, ConvertLowercase, ConvertTitleCase,
		ConvertSentence, ConvertAlternating,
		ConvertLinesToEscaped, ConvertEscapedToLines:
		return true
	default:
		return false
	}
}

// validSignature reports whether sigHex is a valid HMAC-SHA256 of body
// under secret.
//
// hmac.Equal, not ==: a string comparison returns as soon as two bytes
// differ, and that timing difference is enough to recover a valid
// signature one byte at a time. It also covers the length check, so a
// truncated signature fails here rather than needing its own branch.
func validSignature(body []byte, sigHex, secret string) bool {
	got, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
