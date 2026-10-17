package agents

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
)

func strp(s string) *string { return &s }

// update_persona changes the look, then a picture from the caller's
// folder; a path outside it is refused.
func TestCaptainUpdatesAvatar(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	ops := CaptainOps{}
	look := `{"kind":"blob","shape":"cloud","color":"#ef4444","expression":"happy"}`
	if _, err := ops.UpdatePersona(ctx, "s-captain", teamagents.PersonaInput{Agent: "@worker", Avatar: strp(look)}); err != nil {
		t.Fatal(err)
	}
	p, _ := globalTeam.Get(ctx, w.worker.ID)
	av := team.DecodeAvatar(p.Avatar)
	if av.Kind != team.AvatarKindBlob || av.Shape != "cloud" || av.Color != "#ef4444" || av.Expression != "happy" {
		t.Fatalf("avatar = %+v", av)
	}
	if !strings.Contains(convText(t, "s-worker"), "avatar") {
		t.Error("persona_changed must name the avatar")
	}
	if _, err := ops.UpdatePersona(ctx, "s-captain", teamagents.PersonaInput{Agent: "@worker", Avatar: strp("nope")}); err == nil {
		t.Error("non-JSON avatar must fail")
	}

	sess, _ := globalMgr.Registry().Session("s-captain")
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		for y := 0; y < 64; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: 120, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "artifacts", "a.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.UpdatePersona(ctx, "s-captain", teamagents.PersonaInput{Agent: "@worker", AvatarImage: strp("../../etc/passwd")}); err == nil {
		t.Error("a path outside the working folder must be refused")
	}
	if _, err := ops.UpdatePersona(ctx, "s-captain", teamagents.PersonaInput{Agent: "@worker", AvatarImage: strp("artifacts/a.png")}); err != nil {
		t.Fatal(err)
	}
	p, _ = globalTeam.Get(ctx, w.worker.ID)
	av = team.DecodeAvatar(p.Avatar)
	if av.Kind != team.AvatarKindImage || av.Image == nil || av.Image.V == "" {
		t.Fatalf("image avatar = %+v", av)
	}
	if _, err := globalTeam.GetAvatarImage(ctx, w.worker.ID); err != nil {
		t.Errorf("picture not stored: %v", err)
	}

	// Persona off in Settings › Captain refuses the avatar too.
	p.CaptainCan = team.EncodeCaptainCan(team.CaptainCan{Persona: false})
	if err := globalTeam.Update(ctx, &p); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.UpdatePersona(ctx, "s-captain", teamagents.PersonaInput{Agent: "@worker", Avatar: strp(look)}); err == nil {
		t.Error("persona off must refuse an avatar change")
	}
}
