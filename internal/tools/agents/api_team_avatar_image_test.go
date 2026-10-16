package agents

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
)

func avatarTestPNG(t *testing.T) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, 60, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 60; x++ {
			m.Set(x, y, color.NRGBA{uint8(x * 4), uint8(y * 8), 180, 200})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func uploadAvatar(t *testing.T, u *entity.User, id string, raw []byte, ct string) (int, map[string]any) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/api/team/agents/"+id+"/avatar/image",
		map[string]string{"data": base64.StdEncoding.EncodeToString(raw)}, map[string]string{"id": id})
	if ct != "" {
		c.R.Header.Set("Content-Type", ct)
	}
	apiTeamAgentAvatarImageUpload(c)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestAvatarImageUploadSaveAndServe(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "pic", "p1")
	owner := &entity.User{ID: "u1"}

	code, out := uploadAvatar(t, owner, a.ID, avatarTestPNG(t), "application/json")
	if code != http.StatusOK {
		t.Fatalf("upload: %d %v", code, out)
	}
	v, _ := out["v"].(string)
	if len(v) != 16 || out["aspect"].(float64) != 2 {
		t.Fatalf("upload answer: %v", out)
	}

	// Saving the image avatar binds the stored version, whatever the client claims.
	body := map[string]any{"avatar": map[string]any{"kind": "image", "shape": "circle", "color": "#27b199",
		"image": map[string]any{"v": "ffffffffffffffff", "aspect": 99, "fit": "fit", "x": 400, "y": -20, "zoom": 0.01}}}
	w, c := teamReq(t, owner, http.MethodPatch, "/api/team/agents/"+a.ID, body, map[string]string{"id": a.ID})
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	got := team.DecodeAvatar(mustAgent(t, a.ID).Avatar)
	if got.Kind != team.AvatarKindImage || got.Image.V != v || got.Image.Aspect != 2 || got.Image.X != 150 || got.Image.Zoom != 0.3 || got.Image.Fit != "fit" {
		t.Fatalf("stored avatar: %+v %+v", got, got.Image)
	}

	w, c = teamReq(t, owner, http.MethodGet, "/api/team/agents/"+a.ID+"/avatar/image?v="+v, nil, map[string]string{"id": a.ID})
	apiTeamAgentAvatarImage(c)
	h := w.Header()
	if w.Code != http.StatusOK || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || h.Get("ETag") != `"`+v+`"` ||
		h.Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("serve: %d %v", w.Code, h)
	}
	if !bytes.HasPrefix(w.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatal("serve body is not the PNG")
	}
	w, c = teamReq(t, owner, http.MethodGet, "/api/team/agents/"+a.ID+"/avatar/image", nil, map[string]string{"id": a.ID})
	c.R.Header.Set("If-None-Match", `"`+v+`"`)
	apiTeamAgentAvatarImage(c)
	if w.Code != http.StatusNotModified || w.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("conditional: %d %v", w.Code, w.Header())
	}

	// Back to Classic keeps the stored picture and the crop (so Image can
	// come back), and the API hands out the versioned src.
	body = map[string]any{"avatar": map[string]any{"shape": "circle", "color": "#27b199",
		"image": map[string]any{"v": v, "aspect": 2, "fit": "fill", "src": "javascript:alert(1)"}}}
	w, c = teamReq(t, owner, http.MethodPatch, "/api/team/agents/"+a.ID, body, map[string]string{"id": a.ID})
	apiTeamAgentUpdate(c)
	if _, err := globalTeam.GetAvatarImage(context.Background(), a.ID); err != nil {
		t.Fatal("Classic must not delete the stored picture")
	}
	var item TeamAgentItem
	_ = json.Unmarshal(w.Body.Bytes(), &item)
	if item.Avatar.Kind != "" || item.Avatar.Image == nil || item.Avatar.Image.Src != "/tools/agents/api/team/agents/"+a.ID+"/avatar/image?v="+v {
		t.Fatalf("classic answer: %+v %+v", item.Avatar, item.Avatar.Image)
	}
	if strings.Contains(mustAgent(t, a.ID).Avatar, "src") {
		t.Fatal("src must never be stored")
	}
}

// Other create paths cannot start an agent as an image avatar.
func TestAvatarImageCreateIsClassic(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	body := map[string]any{"handle": "newpic", "avatar": map[string]any{"kind": "image", "shape": "circle", "color": "#27b199",
		"image": map[string]any{"v": "0123456789abcdef", "aspect": 1, "fit": "fill"}}}
	w, c := teamReq(t, u, http.MethodPost, "/api/team/agents", body, nil)
	apiTeamAgentCreate(c)
	var item TeamAgentItem
	_ = json.Unmarshal(w.Body.Bytes(), &item)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if item.Avatar.Kind != "" || item.Avatar.Image != nil {
		t.Fatalf("new agent avatar: %+v", item.Avatar)
	}
}

func TestAvatarImageUploadRejects(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "pic", "p1")
	owner := &entity.User{ID: "u1"}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`)
	mismatch := append([]byte("\x89PNG\r\n\x1a\n"), 0xff, 0xd8, 0xff, 0xe0)
	big := append(avatarTestPNG(t), make([]byte, team.AvatarImageMaxInput)...)
	for name, tc := range map[string]struct {
		raw  []byte
		ct   string
		want int
	}{
		"svg":            {svg, "application/json", http.StatusUnsupportedMediaType},
		"magic mismatch": {mismatch, "application/json", http.StatusBadRequest},
		"size cap":       {big, "application/json", http.StatusRequestEntityTooLarge},
		"form post":      {avatarTestPNG(t), "text/plain", http.StatusUnsupportedMediaType},
	} {
		if code, out := uploadAvatar(t, owner, a.ID, tc.raw, tc.ct); code != tc.want {
			t.Errorf("%s: %d %v, want %d", name, code, out, tc.want)
		}
	}
	if _, err := globalTeam.GetAvatarImage(context.Background(), a.ID); err == nil {
		t.Fatal("a refused upload stored a picture")
	}
}

func TestAvatarImageAuthz(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "pic", "p1")
	owner, stranger, friend := &entity.User{ID: "u1"}, &entity.User{ID: "u2"}, &entity.User{ID: "u3"}
	if code, _ := uploadAvatar(t, owner, a.ID, avatarTestPNG(t), "application/json"); code != http.StatusOK {
		t.Fatal("owner upload refused")
	}
	if err := globalTeam.AddShare(context.Background(), a.ID, friend.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	// Writes: owner only. A stranger sees 404, a share recipient 403.
	if code, _ := uploadAvatar(t, stranger, a.ID, avatarTestPNG(t), "application/json"); code != http.StatusNotFound {
		t.Errorf("stranger upload: %d, want 404", code)
	}
	if code, _ := uploadAvatar(t, friend, a.ID, avatarTestPNG(t), "application/json"); code != http.StatusForbidden {
		t.Errorf("recipient upload: %d, want 403", code)
	}
	w, c := teamReq(t, owner, http.MethodPost, "/api/team/agents/"+a.ID+"/avatar/image", map[string]string{"data": ""}, map[string]string{"id": a.ID})
	c.R.Header.Set("Content-Type", "application/json")
	c.R.Header.Set("Sec-Fetch-Site", "cross-site")
	apiTeamAgentAvatarImageUpload(c)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site upload: %d, want 403", w.Code)
	}
	// Reads: owner and recipient, nobody else.
	for u, want := range map[*entity.User]int{owner: http.StatusOK, friend: http.StatusOK, stranger: http.StatusNotFound} {
		w, c := teamReq(t, u, http.MethodGet, "/api/team/agents/"+a.ID+"/avatar/image", nil, map[string]string{"id": a.ID})
		apiTeamAgentAvatarImage(c)
		if w.Code != want {
			t.Errorf("read as %s: %d, want %d", u.ID, w.Code, want)
		}
	}
}
