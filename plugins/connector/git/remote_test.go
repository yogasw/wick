package main

import (
	"strings"
	"testing"
)

func TestStripCredentials(t *testing.T) {
	cases := map[string]string{
		"https://user:token@github.com/org/repo.git": "https://github.com/org/repo.git",
		"https://token@github.com/org/repo.git":      "https://github.com/org/repo.git",
		"https://github.com/org/repo.git":            "https://github.com/org/repo.git",
		"https://user:p%40ss@abc.com/org/repo.git":   "https://abc.com/org/repo.git",
		"git@github.com:org/repo.git":                "git@github.com:org/repo.git", // scp form untouched
		"":                                           "",
	}
	for in, want := range cases {
		if got := StripCredentials(in); got != want {
			t.Errorf("StripCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConvertRemote(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		want      string
		converted bool
	}{
		{"scp form github", "git@github.com:org/repo.git", "https://github.com/org/repo.git", true},
		{"ssh scheme", "ssh://git@github.com/org/repo.git", "https://github.com/org/repo.git", true},
		{"ssh scheme with port", "ssh://git@github.com:22/org/repo.git", "https://github.com/org/repo.git", true},
		{"bitbucket scp", "git@bitbucket.org:team/repo.git", "https://bitbucket.org/team/repo.git", true},
		{"gitlab nested path", "git@gitlab.com:group/sub/repo.git", "https://gitlab.com/group/sub/repo.git", true},
		{"already https", "https://github.com/org/repo.git", "https://github.com/org/repo.git", false},
		{"https with credentials stripped", "https://u:t@github.com/org/repo.git", "https://github.com/org/repo.git", false},
		{"no .git suffix is preserved as-is", "git@github.com:org/repo", "https://github.com/org/repo", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ConvertRemote(c.in, nil, true)
			if err != nil {
				t.Fatalf("ConvertRemote(%q): %v", c.in, err)
			}
			if got.Effective != c.want {
				t.Errorf("Effective = %q, want %q", got.Effective, c.want)
			}
			if got.Converted != c.converted {
				t.Errorf("Converted = %v, want %v", got.Converted, c.converted)
			}
			if got.Original != c.in {
				t.Errorf("Original = %q, want the input preserved", got.Original)
			}
		})
	}
}

func TestConvertRemoteHostMap(t *testing.T) {
	hostMap := map[string]string{
		"git.internal": "code.company.com/git",
		"ssh.abc.net":  "abc.net",
	}
	got, err := ConvertRemote("git@git.internal:team/api.git", hostMap, true)
	if err != nil {
		t.Fatalf("ConvertRemote: %v", err)
	}
	if got.Effective != "https://code.company.com/git/team/api.git" {
		t.Errorf("Effective = %q, want the mapped host with its path prefix", got.Effective)
	}
}

func TestConvertRemoteMustFail(t *testing.T) {
	t.Run("ssh config alias", func(t *testing.T) {
		// "myserver" has no dot: it cannot be a real hostname, so it is an alias
		// from ~/.ssh/config whose real host we cannot know.
		_, err := ConvertRemote("myserver:org/repo.git", nil, true)
		if err == nil {
			t.Fatal("expected an error for an ssh config alias")
		}
		if !strings.Contains(err.Error(), "myserver") {
			t.Errorf("error must name the alias, got: %v", err)
		}
		if !strings.Contains(err.Error(), "remote_host_map") {
			t.Errorf("error must point at remote_host_map, got: %v", err)
		}
	})

	t.Run("conversion disabled", func(t *testing.T) {
		_, err := ConvertRemote("git@github.com:org/repo.git", nil, false)
		if err == nil {
			t.Fatal("expected an error when conversion is disabled for an SSH remote")
		}
		if !strings.Contains(err.Error(), "convert_ssh_remote_to_https") {
			t.Errorf("error must name the setting, got: %v", err)
		}
	})
}

func TestRepoSlug(t *testing.T) {
	cases := map[string]string{
		"https://github.com/org/repo.git":   "github.com/org/repo",
		"git@github.com:org/repo.git":       "github.com/org/repo",
		"https://u:t@abc.com/org/repo.git":  "abc.com/org/repo",
		"git@gitlab.com:group/sub/repo.git": "gitlab.com/group/sub/repo",
		"":                                  "",
	}
	for in, want := range cases {
		if got := RepoSlug(in); got != want {
			t.Errorf("RepoSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseHostMap(t *testing.T) {
	m := ParseHostMap(`[{"ssh_host":"Git.Internal","https_host":"code.company.com/git"},
	                    {"ssh_host":"ssh.abc.net","https_host":"abc.net"}]`)
	// Keys are lowercased so a host written with capitals in the config still
	// matches the host parsed out of a remote URL.
	if m["git.internal"] != "code.company.com/git" {
		t.Errorf("m[git.internal] = %q, want the mapped host", m["git.internal"])
	}
	if m["ssh.abc.net"] != "abc.net" {
		t.Errorf("m[ssh.abc.net] = %q, want abc.net", m["ssh.abc.net"])
	}

	// Rows missing either side can never produce a usable mapping, so they are
	// dropped rather than stored as half a rule.
	if got := ParseHostMap(`[{"ssh_host":"only.host"},{"https_host":"only.https"}]`); len(got) != 0 {
		t.Errorf("ParseHostMap(half rows) = %v, want empty", got)
	}

	// Malformed config degrades to mechanical conversion instead of an error.
	if got := ParseHostMap(`{"ssh_host":"x"}`); got != nil {
		t.Errorf("ParseHostMap(malformed) = %v, want nil", got)
	}
	if got := ParseHostMap(""); len(got) != 0 {
		t.Errorf("ParseHostMap(empty) = %v, want empty", got)
	}
}

func TestConvertRemoteAcceptsLocalPaths(t *testing.T) {
	// A filesystem path is a valid git remote. The scp-like parse splits on the
	// first ":", which on Windows is the drive letter — so C:/repos/x.git used to
	// be read as host "C" and rejected as an ssh alias. Every local-path remote
	// was broken, not just tests.
	cases := []string{
		`C:/repos/mirror.git`,
		`C:\repos\mirror.git`,
		`d:/code/work/origin.git`,
		`/srv/git/repo.git`,
		`file:///srv/git/repo.git`,
		`./local.git`,
		`../sibling.git`,
	}
	for _, in := range cases {
		got, err := ConvertRemote(in, nil, true)
		if err != nil {
			t.Errorf("ConvertRemote(%q) = error %v, want it accepted as a local path", in, err)
			continue
		}
		if got.Effective != in {
			t.Errorf("ConvertRemote(%q).Effective = %q, want the path unchanged", in, got.Effective)
		}
		if got.Converted {
			t.Errorf("ConvertRemote(%q).Converted = true, want false — nothing to convert", in)
		}
	}
}

func TestConvertRemoteLocalPathWorksWithConversionOff(t *testing.T) {
	// convert_ssh_remote_to_https governs SSH remotes. A local path is not SSH, so
	// the setting must not block it.
	got, err := ConvertRemote(`C:/repos/mirror.git`, nil, false)
	if err != nil {
		t.Fatalf("ConvertRemote with conversion off rejected a local path: %v", err)
	}
	if got.Effective != `C:/repos/mirror.git` {
		t.Errorf("Effective = %q, want the path unchanged", got.Effective)
	}
}

func TestLocalPathDetectionDoesNotSwallowSSHRemotes(t *testing.T) {
	// The narrow part of the fix: a real scp-like remote must still be converted,
	// and a single-letter host is not a drive letter unless a separator follows.
	for _, in := range []string{
		"git@github.com:org/repo.git",
		"git@gitlab.com:group/sub/repo.git",
		"ssh://git@abc.com/org/repo.git",
	} {
		got, err := ConvertRemote(in, nil, true)
		if err != nil {
			t.Errorf("ConvertRemote(%q) = error %v, want SSH conversion", in, err)
			continue
		}
		if !got.Converted {
			t.Errorf("ConvertRemote(%q) was not converted — it was mistaken for a local path", in)
		}
	}

	// A hostname that does not exist on disk stays a hostname, so the alias error
	// still fires instead of being silently read as a directory.
	if _, err := ConvertRemote("myserver:org/repo.git", nil, true); err == nil {
		t.Error("an ssh alias was accepted as a local path")
	}
}

func TestParseHostMapStripsScheme(t *testing.T) {
	// The column is labelled "https host", so pasting a full URL is the obvious
	// mistake. Left alone it would build "https://https://code.company.com/..."
	// and push to a host that does not exist.
	cases := map[string]string{
		"https://code.company.com":      "code.company.com",
		"https://code.company.com/git":  "code.company.com/git",
		"https://code.company.com/git/": "code.company.com/git",
		"http://internal.abc.net":       "internal.abc.net",
		"ssh://code.company.com":        "code.company.com",
		"code.company.com":              "code.company.com",
	}
	for in, want := range cases {
		m := ParseHostMap(`[{"ssh_host":"git.internal","https_host":"` + in + `"}]`)
		if got := m["git.internal"]; got != want {
			t.Errorf("https_host %q → %q, want %q", in, got, want)
		}
	}
}

func TestParseHostMapRejectsUnusableValues(t *testing.T) {
	// A value that still carries a scheme separator or userinfo is not a host.
	// Dropping the row is right: ConvertRemote then reports the unmapped alias,
	// which is a readable error, instead of building a nonsense URL.
	for _, bad := range []string{
		"https://https://code.company.com",
		"user@code.company.com",
		"weird://code.company.com",
	} {
		m := ParseHostMap(`[{"ssh_host":"git.internal","https_host":"` + bad + `"}]`)
		if len(m) != 0 {
			t.Errorf("https_host %q produced mapping %v, want the row dropped", bad, m)
		}
	}
}

func TestConvertRemoteWithSchemeInHostMap(t *testing.T) {
	// End to end: a user pastes a full URL into the host map and the push still
	// lands on the right host.
	hostMap := ParseHostMap(`[{"ssh_host":"git.internal","https_host":"https://code.company.com/git"}]`)
	got, err := ConvertRemote("git@git.internal:team/api.git", hostMap, true)
	if err != nil {
		t.Fatalf("ConvertRemote: %v", err)
	}
	if got.Effective != "https://code.company.com/git/team/api.git" {
		t.Errorf("Effective = %q, want a single https:// prefix", got.Effective)
	}
}
