package provider

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func ocIns(name, dir, from string) Instance {
	return Instance{Type: TypeOpencode, Name: name, AuthFrom: from, OpencodeConfig: &OpencodeConfig{DataDir: dir}}
}

func TestValidateAuthFrom(t *testing.T) {
	a := Instance{Type: TypeOMP, Name: "a"}
	b := Instance{Type: TypeOMP, Name: "b", AuthFrom: "a"}
	c := Instance{Type: TypeOMP, Name: "c"}
	oc := Instance{Type: TypeOpencode, Name: "x"}
	all := []Instance{a, b, c, oc}
	cases := []struct {
		name string
		ins  Instance
		want string // "" = ok
	}{
		{"own login", Instance{Type: TypeOMP, Name: "c"}, ""},
		{"ok", Instance{Type: TypeOMP, Name: "c", AuthFrom: "a"}, ""},
		{"self", Instance{Type: TypeOMP, Name: "c", AuthFrom: "c"}, "its own login"},
		{"chain", Instance{Type: TypeOMP, Name: "c", AuthFrom: "b"}, "itself uses the login of a"},
		{"cycle", Instance{Type: TypeOMP, Name: "a", AuthFrom: "b"}, "b uses this instance's login"},
		{"cross type", Instance{Type: TypeOMP, Name: "c", AuthFrom: "x"}, "no omp instance"},
		{"unknown", Instance{Type: TypeOMP, Name: "c", AuthFrom: "zz"}, "no omp instance"},
		{"type without sharing", Instance{Type: TypeClaude, Name: "k", AuthFrom: "a"}, "cannot use another"},
	}
	for _, tc := range cases {
		err := ValidateAuthFrom(all, tc.ins)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	if got := AuthOwnerChoices(all, c); !slices.Equal(got, []string{"a"}) {
		t.Errorf("choices for c = %v, want [a] (not self, not a sharer, not another type)", got)
	}
	if got := AuthOwnerChoices(all, a); got != nil {
		t.Errorf("choices for an owner = %v, want none", got)
	}
	if got := AuthSharers(all, a); !slices.Equal(got, []string{"b"}) {
		t.Errorf("sharers of a = %v", got)
	}
}

func TestAuthOwnerSharesAccountEnv(t *testing.T) {
	owner := Instance{Type: TypeOMP, Name: "own", OMPConfig: &OMPConfig{Profile: "own-p"}}
	sharer := Instance{Type: TypeOMP, Name: "sh", AuthFrom: "own", OMPConfig: &OMPConfig{Profile: "sh-p"}}
	defer SwapAuthInstanceLookup(func(tp Type, n string) (Instance, error) {
		if tp == TypeOMP && n == "own" {
			return owner, nil
		}
		return Instance{}, errors.New("not found")
	})()
	o, ok := AuthOwner(sharer)
	if !ok || o.Name != "own" {
		t.Fatalf("AuthOwner = %s, %v", o.Name, ok)
	}
	// Same account env = same usage identity: one probe for both.
	if !slices.Equal(AccountEnv(sharer), AccountEnv(owner)) {
		t.Errorf("sharer account env %v != owner's %v", AccountEnv(sharer), AccountEnv(owner))
	}
	if !slices.Contains(OwnAccountEnv(sharer), "OMP_PROFILE=sh-p") {
		t.Errorf("OwnAccountEnv lost the sharer's profile: %v", OwnAccountEnv(sharer))
	}
	// A dangling owner falls back to the instance's own login.
	if _, ok := AuthOwner(Instance{Type: TypeOMP, Name: "z", AuthFrom: "gone"}); ok {
		t.Error("unknown owner resolved")
	}
}

func TestOpencodeAuthLink(t *testing.T) {
	root := t.TempDir()
	owner := ocIns("own", filepath.Join(root, "own"), "")
	sharer := ocIns("sh", filepath.Join(root, "sh"), "own")
	all := []Instance{owner, ocIns("sh", sharer.OpencodeConfig.DataDir, "")}
	ownerAuth, _ := OpencodeAuthFile(owner)
	sharerAuth, _ := OpencodeAuthFile(sharer)

	// Absent: linked, and the owner's dir exists for the link to write to.
	if err := applyAuthFromChange(all, sharer, ""); err != nil {
		t.Fatal(err)
	}
	if cur, err := os.Readlink(sharerAuth); err != nil || cur != ownerAuth {
		t.Fatalf("link = %q, %v; want %q", cur, err, ownerAuth)
	}
	if err := os.WriteFile(ownerAuth, []byte(`{"openai":{"type":"oauth"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(sharerAuth); !strings.Contains(string(b), "openai") {
		t.Errorf("sharer does not read the owner's login: %s", b)
	}

	// Removing auth_from removes the link only.
	sharer.AuthFrom = ""
	if err := applyAuthFromChange(all, sharer, "own"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(sharerAuth); !os.IsNotExist(err) {
		t.Errorf("link still there: %v", err)
	}
	if _, err := os.Stat(ownerAuth); err != nil {
		t.Errorf("owner's auth.json gone: %v", err)
	}

	// An empty auth.json is replaced; a real login is refused and kept.
	sharer.AuthFrom = "own"
	_ = os.WriteFile(sharerAuth, []byte("{}"), 0o600)
	if err := applyAuthFromChange(all, sharer, ""); err != nil {
		t.Fatalf("empty auth.json refused: %v", err)
	}
	_ = os.Remove(sharerAuth)
	_ = os.WriteFile(sharerAuth, []byte(`{"anthropic":{"type":"api","key":"x"}}`), 0o600)
	err := applyAuthFromChange(all, sharer, "")
	if err == nil || !strings.Contains(err.Error(), "log out of this instance first") {
		t.Fatalf("own login not refused: %v", err)
	}
	if b, _ := os.ReadFile(sharerAuth); !strings.Contains(string(b), "anthropic") {
		t.Error("sharer's own credentials were touched")
	}
}
