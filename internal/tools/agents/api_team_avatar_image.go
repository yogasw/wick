package agents

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// avatarImageUploadReq is POST /api/team/agents/{id}/avatar/image: the
// picture as standard base64 in a JSON body. JSON, like every other write
// of this API, keeps the request off the cross-site "simple request" list
// (a form post cannot send it), which together with the SameSite=Lax
// session cookie is the app's CSRF guard.
type avatarImageUploadReq struct {
	Data string `json:"data"`
}

// agentsToolBase is where the Agents tool is mounted; the avatar URL the
// API hands out (team.WithImageSrc) is under it.
const agentsToolBase = "/tools/agents"

// avatarImageMaxBody is the JSON body cap: base64 of the 2 MiB input plus
// room for the envelope.
const avatarImageMaxBody = team.AvatarImageMaxInput/3*4 + 4<<10

// apiTeamAgentAvatarImageUpload handles POST /api/team/agents/{id}/avatar/image.
// Owner only (the same gate as every other settings write). It stores the
// re-encoded picture and answers its version and aspect; the avatar itself
// switches to it when Settings saves an "image" avatar with that version.
func apiTeamAgentAvatarImageUpload(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	if c.R.Header.Get("Sec-Fetch-Site") == "cross-site" {
		c.JSON(http.StatusForbidden, map[string]string{"error": "cross-site request refused"})
		return
	}
	if mt, _, err := mime.ParseMediaType(c.R.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		c.JSON(http.StatusUnsupportedMediaType, map[string]string{"error": "send the image as JSON {\"data\": base64}"})
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	var req avatarImageUploadReq
	body := http.MaxBytesReader(c.W, c.R.Body, avatarImageMaxBody)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": team.ErrAvatarImageTooLarge.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if base64.StdEncoding.DecodedLen(len(req.Data)) > team.AvatarImageMaxInput+3 {
		c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": team.ErrAvatarImageTooLarge.Error()})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil || len(raw) == 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "data must be a base64 image"})
		return
	}
	img, err := team.ProcessAvatarImageLimited(c.Context(), raw)
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, team.ErrAvatarImageTooLarge), errors.Is(err, team.ErrAvatarImageOutTooBig):
			status = http.StatusRequestEntityTooLarge
		case errors.Is(err, team.ErrAvatarImageType), errors.Is(err, team.ErrAvatarImageWebP):
			status = http.StatusUnsupportedMediaType
		}
		c.JSON(status, map[string]string{"error": err.Error()})
		return
	}
	if err := globalTeam.SaveAvatarImage(c.Context(), p.ID, img); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not save the image"})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"v": img.Version, "aspect": float64(img.Width) / float64(img.Height),
		"width": img.Width, "height": img.Height,
	})
}

// apiTeamAgentAvatarImage handles GET /api/team/agents/{id}/avatar/image:
// the stored picture, to whoever may see the agent — its owner or a person
// it is shared with (agentChatMember), the same people its roster entry
// reaches. Served as an image that can never run as a document.
func apiTeamAgentAvatarImage(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, err := globalTeam.Get(c.Context(), c.PathValue("id"))
	if err != nil || !agentChatMember(c.Context(), p, actorID(c)) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}
	row, err := globalTeam.GetAvatarImage(c.Context(), p.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no avatar image"})
		return
	}
	writeAvatarImage(c.W, c.R, row)
}

// writeAvatarImage sends row with the headers that keep it an inert
// picture: a server-chosen type, no sniffing, a CSP that runs nothing,
// and a cache keyed on the version (immutable when ?v= names it).
func writeAvatarImage(w http.ResponseWriter, r *http.Request, row entity.AgentAvatarImage) {
	h := w.Header()
	ct := "image/png"
	if row.ContentType == "image/jpeg" {
		ct = "image/jpeg"
	}
	etag := `"` + row.Version + `"`
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", `inline; filename="avatar"`)
	h.Set("ETag", etag)
	if r.URL.Query().Get("v") == row.Version {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(row.Data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(row.Data)
	}
}

// bindAvatarImage makes an avatar's crop point at the picture actually
// stored for agent id: version and aspect come from the row, never from
// the request. No stored picture drops the crop, and NormalizeAvatar then
// turns an "image" kind back into classic. It never deletes: the stored
// picture goes only with the agent or when a new upload replaces it, so
// switching to Classic and back keeps it.
func bindAvatarImage(c *tool.Ctx, id string, a *team.Avatar) {
	if a.Image == nil {
		return
	}
	row, err := globalTeam.GetAvatarImage(c.Context(), id)
	if err != nil {
		a.Image = nil
		return
	}
	im := *a.Image
	im.V = row.Version
	im.Aspect = float64(row.Width) / float64(max(1, row.Height))
	a.Image = &im
}
