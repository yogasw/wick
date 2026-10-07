package pool

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
)

// holdingSpawner starts a process that stays alive until killed, so a test
// can read ActiveSnapshot while the spawn is running.
type holdingSpawner struct{}

func (holdingSpawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	pr, pw := io.Pipe()
	proc := &scriptedProc{
		stdoutR:  pr,
		stdoutW:  pw,
		stdinBuf: &bytes.Buffer{},
		opt:      opt,
		done:     make(chan struct{}),
		pid:      71000,
	}
	go func() { _, _ = pw.Write([]byte(`{"type":"system","subtype":"init","session_id":"abc"}` + "\n")) }()
	return proc, nil
}

// mintedFactory stands in for a factory whose minter picked runAs: it reports
// that identity the way ClaudeFactory does after minting.
type mintedFactory struct {
	inner *ClaudeFactory
	runAs string
}

func (f *mintedFactory) Build(opt FactoryOptions) (BuildResult, error) {
	br, err := f.inner.Build(opt)
	br.RunAsUserID = f.runAs
	return br, err
}

// snapshotOwner spawns session S1 from ctx and returns the identity the
// running-agents view would be handed for it.
func snapshotOwner(t *testing.T, ctx context.Context, owner, minted string) string {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	inner := &ClaudeFactory{Layout: layout, Spawner: holdingSpawner{}}
	p := New(PoolConfig{
		MaxConcurrent: 2,
		IdleTimeout:   time.Minute,
		Layout:        layout,
		Factory:       &mintedFactory{inner: inner, runAs: minted},
		CallerUserID:  poolWithCaller().cfg.CallerUserID,
	})
	inner.OnExit = p.HandleExit
	t.Cleanup(p.Stop)
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "S1", Origin: session.OriginUI, UserID: owner,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "S1", "default", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(ctx, "S1", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	var got []ActiveEntry
	waitFor(t, func() bool {
		got = p.ActiveSnapshot()
		return len(got) == 1 && got[0].PID != 0
	}, 2*time.Second)
	return got[0].CallerUserID
}

// A main session woken by no human — a sub-agent's result, a scheduled
// message — has no caller on its context, yet its credential is still minted
// for the owner. The running-agents view showed such a spawn with no name
// while its own sub-agents carried one.
func TestActiveSnapshotNamesMintedIdentityWhenNoCallerWoke(t *testing.T) {
	if got := snapshotOwner(t, ctxAs(""), "user-own", "user-own"); got != "user-own" {
		t.Fatalf("CallerUserID = %q, want user-own — the identity the credential was minted for", got)
	}
}

// With no minted identity (shared token, a provider with no MCP surface) the
// session owner is still whose agent this is.
func TestActiveSnapshotFallsBackToSessionOwner(t *testing.T) {
	if got := snapshotOwner(t, ctxAs(""), "user-own", ""); got != "user-own" {
		t.Fatalf("CallerUserID = %q, want the session owner", got)
	}
}

// A human-triggered turn keeps showing who asked, as before.
func TestActiveSnapshotPrefersCallerOverOwner(t *testing.T) {
	if got := snapshotOwner(t, ctxAs("user-b"), "user-own", ""); got != "user-b" {
		t.Fatalf("CallerUserID = %q, want the caller user-b", got)
	}
}

// Nobody asked and nobody owns the session: silence is the honest answer.
func TestActiveSnapshotLeavesOwnerlessSpawnBlank(t *testing.T) {
	if got := snapshotOwner(t, ctxAs(""), "", ""); got != "" {
		t.Fatalf("CallerUserID = %q, want empty for an ownerless spawn no human woke", got)
	}
}

// The factory reports the identity the minter chose, and none for the shared
// internal token — that token belongs to no human.
func TestMCPCredentialForReportsMintedIdentity(t *testing.T) {
	var seen []string
	f := &ClaudeFactory{MCPToken: "internal-token", SessionMCPToken: mintFor(&seen)}

	if tok, id := f.mcpCredentialFor("sess-1", ""); tok != "token-for-owner-of-sess-1" || id != "owner-of-sess-1" {
		t.Fatalf("got (%q, %q), want the owner's token and identity", tok, id)
	}
	f.SessionMCPToken = func(string, string) (string, string, bool) { return "", "", false }
	if tok, id := f.mcpCredentialFor("sess-1", ""); tok != "internal-token" || id != "" {
		t.Fatalf("got (%q, %q), want internal token with no identity", tok, id)
	}
}
