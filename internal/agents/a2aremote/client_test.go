package a2aremote

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/a2aremote/a2aremotetest"
)

// local allows the httptest host, which the default policy refuses.
var local = Guard{Allowed: []string{"127.0.0.1"}}

type fakeCodec struct{}

func (fakeCodec) EncryptSecret(p string) (string, error) { return "wick_enc_" + reverse(p), nil }
func (fakeCodec) DecryptSecret(t string) (string, error) {
	if !strings.HasPrefix(t, "wick_enc_") {
		return "", errors.New("bad token " + t)
	}
	return reverse(strings.TrimPrefix(t, "wick_enc_")), nil
}

func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func TestCheckURL(t *testing.T) {
	open := Guard{}
	for _, bad := range []string{
		"ftp://example.com", "example.com/x", "http://127.0.0.1:8080", "http://localhost", "http://[::1]/",
		"http://169.254.169.254/latest", "http://metadata.google.internal", "http://0.0.0.0", "https://u:p@example.com",
	} {
		if _, err := open.CheckURL(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	for _, ok := range []string{"https://agents.example.com/.well-known/agent-card.json", "http://10.1.2.3:9000"} {
		if _, err := open.CheckURL(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	listed := Guard{Allowed: ParseAllowlist("127.0.0.1, *.corp.example\n")}
	for raw, want := range map[string]bool{
		"http://127.0.0.1:1": true, "https://a.corp.example": true, "https://example.com": false, "http://169.254.169.254": false,
	} {
		if _, err := listed.CheckURL(raw); (err == nil) != want {
			t.Errorf("allowlist %s: err=%v want ok=%v", raw, err, want)
		}
	}
}

// TestDialGuard: a name that passes CheckURL still cannot dial a blocked
// address — the check runs on the resolved IP.
func TestDialGuard(t *testing.T) {
	srv := a2aremotetest.New("x")
	defer srv.Close()
	_, err := Guard{}.HTTPClient(PlainAuth{}, 0).Get(srv.URL)
	if err == nil || !errors.Is(err, ErrBlockedHost) {
		t.Fatalf("dial to loopback: %v", err)
	}
	if _, err := Resolve(context.Background(), Guard{}, srv.URL, PlainAuth{}); !errors.Is(err, ErrBlockedHost) {
		t.Fatalf("resolve loopback: %v", err)
	}
}

func TestResolveAndPingWithAuth(t *testing.T) {
	srv := a2aremotetest.New("Research Bot")
	srv.Bearer = "s3cret-token"
	defer srv.Close()
	ctx := context.Background()

	if _, err := Resolve(ctx, local, srv.URL, PlainAuth{Type: AuthNone}); err == nil {
		t.Fatal("card resolved without the bearer")
	}
	auth := PlainAuth{Type: AuthBearer, Secret: "s3cret-token"}
	for _, u := range []string{srv.URL, srv.URL + "/", srv.URL + "/.well-known/agent-card.json"} {
		res, err := Resolve(ctx, local, u, auth)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		snap := SnapshotOf(res.Card)
		if snap.Name != "Research Bot" || !snap.Streaming || len(snap.Skills) != 1 || res.CardURL != srv.URL+"/.well-known/agent-card.json" || len(res.JSON) == 0 {
			t.Fatalf("%s: %+v %s", u, snap, res.CardURL)
		}
	}
	res, _ := Resolve(ctx, local, srv.URL, auth)
	out := Ping(ctx, local, res.Card, auth)
	if !out.OK || out.Reply != "echo: ping" {
		t.Fatalf("ping: %+v", out)
	}
	if strings.Contains(auth.String(), "s3cret") {
		t.Fatal("PlainAuth.String leaks the secret")
	}
}

func TestResolveRefusesInternalEndpoint(t *testing.T) {
	srv := a2aremotetest.New("x")
	srv.Endpoint = "http://169.254.169.254/rpc"
	defer srv.Close()
	if _, err := Resolve(context.Background(), local, srv.URL, PlainAuth{}); !errors.Is(err, ErrBlockedHost) {
		t.Fatalf("card pointing at metadata: %v", err)
	}
}

func TestSealRoundTrip(t *testing.T) {
	a, err := NormalizeAuth(PlainAuth{Type: "API_KEY", Secret: " k1 "})
	if err != nil || a.Header != DefaultAPIKeyHeader || a.Secret != "k1" {
		t.Fatalf("normalize: %+v %v", a, err)
	}
	sealed, err := Seal(fakeCodec{}, a)
	if err != nil || sealed.Secret == "k1" || !sealed.Set() {
		t.Fatalf("seal: %+v %v", sealed, err)
	}
	back, err := sealed.Plain(fakeCodec{})
	if err != nil || back.Secret != "k1" || back.Header != DefaultAPIKeyHeader {
		t.Fatalf("plain: %+v %v", back, err)
	}
	if _, err := NormalizeAuth(PlainAuth{Type: AuthBearer}); err == nil {
		t.Fatal("bearer without secret accepted")
	}
	if _, err := NormalizeAuth(PlainAuth{Type: AuthAPIKey, Header: "X Bad", Secret: "k"}); err == nil {
		t.Fatal("bad header accepted")
	}
}

func TestCappedBody(t *testing.T) {
	b := &cappedBody{rc: io.NopCloser(strings.NewReader(strings.Repeat("a", 100))), left: 10}
	got, err := io.ReadAll(b)
	if len(got) != 10 || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %d bytes, %v", len(got), err)
	}
}

func TestValidateLimits(t *testing.T) {
	if err := ValidateLimits(0, 0, ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		t int
		b int64
		u string
	}{{-1, 0, ""}, {MaxTimeoutSec + 1, 0, ""}, {0, 10, ""}, {0, 0, "everyone"}} {
		if ValidateLimits(c.t, c.b, c.u) == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}
