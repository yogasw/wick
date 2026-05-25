// Package rest — OpenAI-compatible GET /v1/models handler.
//
// The list is built live from configured wick providers (claude / codex
// / gemini and their instances). Disabled instances are excluded. Each
// enabled instance shows up once: the default-seeded entry whose
// Name == string(Type) advertises as the bare type id ("claude"); named
// instances advertise as "<type>/<name>" ("claude/work").
//
// Chat / Responses handlers call IsModelAllowed against the same source
// — if a client sends a `model` that is not in this list, the request
// is rejected with an OpenAI-shaped model_not_found error so SDK error
// handling stays consistent with the real API.

package rest

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// modelObject is one entry in the /v1/models list. Mirrors OpenAI's
// shape: {id, object, created, owned_by}.
type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// modelsListResponse is the top-level envelope for /v1/models.
type modelsListResponse struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

// modelLoader returns the live provider instances backing the model
// catalogue. Indirect-via-var so tests can stub the source without
// touching userconfig on disk. Production callers leave it pointing at
// provider.Load.
var modelLoader = func() ([]provider.Instance, error) { return provider.Load() }

// availableModels enumerates every enabled provider instance as an
// OpenAI model object. Returns at least one entry only when the user
// has a non-disabled instance configured; an empty slice signals "no
// providers active" so /v1/models returns `data: []` and chat/responses
// reject any model id.
func availableModels() []modelObject {
	insts, err := modelLoader()
	if err != nil {
		return nil
	}
	now := time.Now().Unix()
	out := make([]modelObject, 0, len(insts))
	for _, ins := range insts {
		if ins.Disabled {
			continue
		}
		out = append(out, modelObject{
			ID:      modelIDFor(ins),
			Object:  "model",
			Created: now,
			OwnedBy: string(ins.Type),
		})
	}
	return out
}

// modelIDFor renders the advertised id for one instance. The
// default-seeded entry (Name == string(Type)) collapses to the bare
// type so a client passing model="claude" works out of the box; named
// instances surface as "<type>/<name>" so wick users with multiple
// profiles can pick a specific one.
func modelIDFor(ins provider.Instance) string {
	if ins.Name == "" || ins.Name == string(ins.Type) {
		return string(ins.Type)
	}
	return string(ins.Type) + "/" + ins.Name
}

// IsModelAllowed reports whether id matches any currently-enabled
// provider instance. Empty id is allowed and treated as "wick will
// pick" by the caller — explicit ids must match exactly.
func IsModelAllowed(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return true
	}
	for _, m := range availableModels() {
		if m.ID == id {
			return true
		}
	}
	return false
}

// writeModelNotFound writes an OpenAI-shaped 404 model_not_found error.
// Mirrors the exact body openai-python / openai-node clients parse so
// their typed APIError fires with the right code.
func writeModelNotFound(w http.ResponseWriter, requested string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": "The model `" + requested + "` does not exist or is not enabled on this wick. Call GET /v1/models to list available providers.",
			"type":    "invalid_request_error",
			"param":   "model",
			"code":    "model_not_found",
		},
	})
}

// handleModels serves GET /v1/models. Requires the same Bearer auth as
// chat / responses so the catalogue isn't world-readable.
func (c *Channel) handleModels(w http.ResponseWriter, r *http.Request) {
	if status, msg := c.checkReady(); status != 0 {
		writeError(w, status, msg)
		return
	}
	if _, status, msg := c.authBearer(r); status != 0 {
		writeError(w, status, msg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(modelsListResponse{
		Object: "list",
		Data:   availableModels(),
	})
}
