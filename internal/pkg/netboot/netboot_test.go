package netboot

import (
	"reflect"
	"testing"
)

func TestSetupIdempotent(t *testing.T) {
	Setup()
	Setup()
	Setup()
	if setupCount != 1 {
		t.Fatalf("Setup ran %d times, want exactly 1 (must be idempotent across all entry points)", setupCount)
	}
}

func TestHasConfiguredNameserver(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"loopback respected (systemd-resolved)", "nameserver 127.0.0.53\n", true},
		{"ipv6 loopback respected", "nameserver ::1\n", true},
		{"public", "nameserver 8.8.8.8\n", true},
		{"comment plus public", "# generated\nnameserver 1.1.1.1\n", true},
		{"no nameserver line", "search lan\noptions ndots:1\n", false},
		{"malformed nameserver", "nameserver notanip\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasConfiguredNameserver(tc.in); got != tc.want {
				t.Errorf("hasConfiguredNameserver(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeServer(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"   ":          "",
		"1.1.1.1":      "1.1.1.1:53",
		"1.1.1.1:5353": "1.1.1.1:5353",
		"  8.8.8.8 ":   "8.8.8.8:53",
	}
	for in, want := range cases {
		if got := normalizeServer(in); got != want {
			t.Errorf("normalizeServer(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseServers(t *testing.T) {
	got := parseServers("1.1.1.1, 8.8.8.8 9.9.9.9:5353")
	want := []string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:5353"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseServers = %v, want %v", got, want)
	}
	if got := parseServers("  ,  "); len(got) != 0 {
		t.Errorf("parseServers(blank) = %v, want empty", got)
	}
}

func TestUsableNameservers(t *testing.T) {
	got := usableNameservers("# comment\nnameserver 127.0.0.1\nnameserver 192.168.1.1\nnameserver ::1\nnameserver 8.8.8.8\nsearch lan\n")
	want := []string{"192.168.1.1:53", "8.8.8.8:53"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("usableNameservers = %v, want %v", got, want)
	}
	if got := usableNameservers("nameserver 127.0.0.1\nnameserver ::1\n"); len(got) != 0 {
		t.Errorf("loopback-only should yield empty, got %v", got)
	}
}

func TestChooseNameservers(t *testing.T) {
	if got := chooseNameservers("1.1.1.1", []string{"10.0.0.1:53"}, []string{"8.8.8.8"}); !reflect.DeepEqual(got, []string{"1.1.1.1:53"}) {
		t.Errorf("override should win: %v", got)
	}
	if got := chooseNameservers("", []string{"10.0.0.1:53"}, []string{"8.8.8.8"}); !reflect.DeepEqual(got, []string{"10.0.0.1:53"}) {
		t.Errorf("$PREFIX resolv.conf should win over android: %v", got)
	}
	if got := chooseNameservers("", nil, []string{"192.168.1.1", "8.8.4.4"}); !reflect.DeepEqual(got, []string{"192.168.1.1:53", "8.8.4.4:53"}) {
		t.Errorf("android dns should be used: %v", got)
	}
	if got := chooseNameservers("", nil, nil); !reflect.DeepEqual(got, defaultNameservers) {
		t.Errorf("defaults expected: %v", got)
	}
}
