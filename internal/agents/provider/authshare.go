package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// authshare.go lets several omp / opencode instances share ONE login while
// each keeps its own profile, config, soul and sessions. A sharer names the
// owner in Instance.AuthFrom; the owner is an instance of the same type that
// does not share itself (no chains, no cycles).
//
//   - omp: native auth broker. wick runs `omp --profile <owner> auth-broker
//     serve` and hands sharers OMP_AUTH_BROKER_URL / OMP_AUTH_BROKER_TOKEN
//     (omp/broker.go); they keep their own --profile.
//   - opencode: no native option; auth.json always lives in
//     $XDG_DATA_HOME/opencode. opencode rewrites it in place (writeFile, no
//     rename), so the sharer's auth.json is a symlink to the owner's and
//     survives a token refresh. There is no cross-process lock: two
//     instances refreshing a rotating refresh token at once can race.
//
// Reads of the login (account, usage, account list, login TTY) resolve the
// owner through AccountEnv, so a sharer reports the owner's identity and
// shares its usage probe.

// SharesAuth reports whether instances of t can use another's login.
func SharesAuth(t Type) bool { return t == TypeOMP || t == TypeOpencode }

// findInstance resolves the owner, loadInstances lists every instance;
// swapped in tests.
var (
	findInstance  = Find
	loadInstances = Load
)

// AuthOwner is the instance whose login ins uses, and true, when ins is a
// sharer whose owner resolves; else ins itself and false.
func AuthOwner(ins Instance) (Instance, bool) {
	if ins.AuthFrom == "" || !SharesAuth(ins.Type) {
		return ins, false
	}
	o, err := findInstance(ins.Type, ins.AuthFrom)
	if err != nil || o.Type != ins.Type || o.Name == ins.Name || o.AuthFrom != "" {
		return ins, false
	}
	return o, true
}

// authOwnerOrSelf is AuthOwner without the flag.
func authOwnerOrSelf(ins Instance) Instance {
	o, _ := AuthOwner(ins)
	return o
}

// ValidateAuthFrom checks ins.AuthFrom against every configured instance
// (all may hold ins's old version; it is ignored). "" = fine.
func ValidateAuthFrom(all []Instance, ins Instance) error {
	var sharers []string
	for _, o := range all {
		if o.Type == ins.Type && o.Name != ins.Name && o.AuthFrom == ins.Name {
			sharers = append(sharers, o.Name)
		}
	}
	if ins.AuthFrom == "" {
		return nil
	}
	if !SharesAuth(ins.Type) {
		return fmt.Errorf("%s instances cannot use another instance's login", ins.Type)
	}
	if ins.AuthFrom == ins.Name {
		return errors.New("an instance cannot use its own login as a shared login")
	}
	if len(sharers) > 0 {
		return fmt.Errorf("%s uses this instance's login, so it cannot use another's", sharers[0])
	}
	for _, o := range all {
		if o.Name != ins.AuthFrom {
			continue
		}
		if o.Type != ins.Type {
			continue
		}
		if o.AuthFrom != "" {
			return fmt.Errorf("%s itself uses the login of %s; pick %s instead", o.Name, o.AuthFrom, o.AuthFrom)
		}
		return nil
	}
	return fmt.Errorf("no %s instance named %q", ins.Type, ins.AuthFrom)
}

// AuthOwnerChoices are the instances ins may take its login from: same
// type, not ins, not sharing themselves. Empty when others already use
// ins's login (it cannot become a sharer).
func AuthOwnerChoices(all []Instance, ins Instance) []string {
	if !SharesAuth(ins.Type) {
		return nil
	}
	var out []string
	for _, o := range all {
		if o.Type != ins.Type || o.Name == ins.Name {
			continue
		}
		if o.AuthFrom == ins.Name {
			return nil
		}
		if o.AuthFrom == "" {
			out = append(out, o.Name)
		}
	}
	return out
}

// AuthSharers lists the instances using ins's login.
func AuthSharers(all []Instance, ins Instance) []string {
	var out []string
	for _, o := range all {
		if o.Type == ins.Type && o.Name != ins.Name && o.AuthFrom == ins.Name {
			out = append(out, o.Name)
		}
	}
	return out
}

// applyAuthFromChange validates a changed AuthFrom and moves the opencode
// symlink before the change is persisted; nothing is saved on error.
func applyAuthFromChange(all []Instance, ins Instance, prev string) error {
	if ins.AuthFrom == prev {
		return nil
	}
	if err := ValidateAuthFrom(all, ins); err != nil {
		return err
	}
	if ins.Type != TypeOpencode {
		return nil
	}
	// Another owner, or none: the old links go first (extra-account
	// links are made again by the next spawn).
	if prev != "" {
		if err := UnlinkOpencodeAuth(ins); err != nil {
			return err
		}
	}
	if ins.AuthFrom == "" {
		return nil
	}
	for _, o := range all {
		if o.Type == ins.Type && o.Name == ins.AuthFrom {
			return LinkOpencodeAuth(ins, o)
		}
	}
	return fmt.Errorf("no %s instance named %q", ins.Type, ins.AuthFrom)
}

// LinkOpencodeAuth makes sharer's main auth.json a symlink to owner's. It
// refuses when the sharer holds a login of its own: credentials are never
// deleted, the user logs out first. A previous symlink is replaced.
func LinkOpencodeAuth(sharer, owner Instance) error {
	src, err := OpencodeAuthFile(owner)
	if err != nil {
		return err
	}
	dst, err := OpencodeAuthFile(sharer)
	if err != nil {
		return err
	}
	return linkAuthFile(sharer.Name, src, dst)
}

// EnsureOpencodeAuthLink (spawn path) links the sharer's folder for account
// to the owner's same account. A no-op for an instance with its own login.
func EnsureOpencodeAuthLink(sharer Instance, account string) error {
	owner, ok := AuthOwner(sharer)
	if !ok || sharer.Type != TypeOpencode {
		return nil
	}
	s, err := WithOpencodeAccount(sharer, account)
	if err != nil {
		return err
	}
	o, err := WithOpencodeAccount(owner, account)
	if err != nil {
		return err
	}
	return LinkOpencodeAuth(s, o)
}

func linkAuthFile(name, src, dst string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return fmt.Errorf("opencode instance %s: owner and sharer have the same data dir", name)
	}
	if cur, err := os.Readlink(dst); err == nil && cur == src {
		return nil
	}
	if fi, err := os.Lstat(dst); err == nil && fi.Mode()&os.ModeSymlink == 0 {
		b, rerr := os.ReadFile(dst)
		if rerr != nil {
			return fmt.Errorf("opencode instance %s: read auth.json: %w", name, rerr)
		}
		if !emptyAuthJSON(b) {
			return fmt.Errorf("opencode instance %s already has its own login — log out of this instance first", name)
		}
	}
	// opencode writes through the link, so the target's dir must exist
	// even before the owner ever logged in.
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(src, dst)
}

// emptyAuthJSON: blank, or a JSON object with no provider in it.
func emptyAuthJSON(b []byte) bool {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return true
	}
	var m map[string]json.RawMessage
	return json.Unmarshal(b, &m) == nil && len(m) == 0
}

// UnlinkOpencodeAuth removes sharer's auth.json symlinks — main and the
// extra-account folders spawn linked — leaving the owner's files and any
// real file alone. An account folder left empty is removed with its link.
func UnlinkOpencodeAuth(sharer Instance) error {
	sharer.AuthFrom = ""
	dst, err := OpencodeAuthFile(sharer)
	if err != nil {
		return err
	}
	if err := removeIfSymlink(dst); err != nil {
		return err
	}
	for _, id := range OpencodeAccountIDs(sharer)[1:] {
		acc, err := WithOpencodeAccount(sharer, id)
		if err != nil {
			continue
		}
		p, err := OpencodeAuthFile(acc)
		if err != nil {
			continue
		}
		if err := removeIfSymlink(p); err != nil {
			return err
		}
		_ = os.Remove(filepath.Dir(p))               // <acc>/opencode, only when empty
		_ = os.Remove(filepath.Dir(filepath.Dir(p))) // <acc>, only when empty
	}
	return nil
}

func removeIfSymlink(p string) error {
	fi, err := os.Lstat(p)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	return os.Remove(p)
}

// SwapAuthInstanceLookup replaces how owners are resolved (tests in other
// packages); returns the restore.
func SwapAuthInstanceLookup(find func(Type, string) (Instance, error)) func() {
	prev := findInstance
	findInstance = find
	return func() { findInstance = prev }
}
