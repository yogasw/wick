package connectors

import (
	"testing"

	"github.com/yogasw/wick/internal/enc"
)

func TestResealForRunner(t *testing.T) {
	svc, err := enc.New(testEncKey)
	if err != nil {
		t.Fatal(err)
	}
	const owner, runner = "user-owner", "user-runner"
	tok, err := svc.EncryptValue("s3cret", owner)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := svc.EncryptValue("other", "user-stranger")
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]string{"secret_key": tok, "app_code": "acme", "x": foreign}

	if _, err := svc.DecryptValue(tok, runner); err == nil {
		t.Fatal("runner should not open owner's token")
	}
	out := ResealForRunner(svc, in, owner, runner)
	if !enc.IsMasterToken(out["secret_key"]) {
		t.Fatalf("secret_key not resealed: %q", out["secret_key"])
	}
	if plain, err := svc.DecryptMaster(out["secret_key"]); err != nil || plain != "s3cret" {
		t.Fatalf("master decrypt = %q, %v", plain, err)
	}
	if out["app_code"] != "acme" || out["x"] != foreign {
		t.Fatalf("untouched values changed: %+v", out)
	}
	if in["secret_key"] != tok {
		t.Fatal("input map mutated")
	}
	if same := ResealForRunner(svc, in, owner, owner); same["secret_key"] != tok {
		t.Fatal("same user must be a no-op")
	}
}
