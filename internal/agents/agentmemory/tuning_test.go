package agentmemory

import (
	"reflect"
	"testing"
)

func TestNonLoopbackHostsIgnoresTheSafeOnes(t *testing.T) {
	cases := []struct {
		name  string
		hosts string
		want  []string
	}{
		{"empty is nothing", "", nil},
		{"the loopback default", "localhost, 127.0.0.1, ::1", nil},
		{"case does not hide a loopback name", "LocalHost,127.0.0.1", nil},
		{"a LAN name is reported back verbatim", "localhost,homelab", []string{"homelab"}},
		{"several, in order", "127.0.0.1,192.168.0.90,memory.internal", []string{"192.168.0.90", "memory.internal"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tuning{AllowedHosts: tc.hosts}.NonLoopbackHosts()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("NonLoopbackHosts(%q) = %v, want %v", tc.hosts, got, tc.want)
			}
		})
	}
}

// The combination with no safe reading: reachable by name AND no token.
func TestExposedWithoutAuth(t *testing.T) {
	cases := []struct {
		name  string
		hosts string
		token string
		want  bool
	}{
		{"loopback, no token — the normal local setup", "localhost,127.0.0.1", "", false},
		{"LAN host with a token — deliberate and defended", "homelab", "secret", false},
		{"LAN host, no token — the store is readable by anything that can route", "homelab", "", true},
		{"a whitespace-only token is no token", "homelab", "   ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tu := Tuning{AllowedHosts: tc.hosts, AuthToken: tc.token}
			if got := tu.ExposedWithoutAuth(); got != tc.want {
				t.Fatalf("ExposedWithoutAuth() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLinesAndCSVDropBlanks(t *testing.T) {
	if got, want := Lines("a\n\n  b  \n"), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Lines = %v, want %v", got, want)
	}
	if got, want := CSV(" a , ,b,"), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CSV = %v, want %v", got, want)
	}
	if Lines("   ") != nil {
		t.Fatal("a field holding only whitespace must yield no entries, not one empty one")
	}
}

// A masked token must never be written back as the literal bullets — that
// would replace a working credential with a string of dots on the next save.
func TestMaskSecret(t *testing.T) {
	if maskSecret("") != "" {
		t.Fatal("an unset token masks to empty, so the form shows an empty field")
	}
	if maskSecret("   ") != "" {
		t.Fatal("whitespace is not a credential")
	}
	if got := maskSecret("hunter2"); got != secretMask {
		t.Fatalf("maskSecret = %q, want the placeholder", got)
	}
}
