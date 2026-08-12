package channels

import (
	"context"
	"testing"
)

// resolveProject mirrors the precedence the pool send closure in
// api/server.go applies on every dispatch. Kept here so the ordering is
// pinned by a test that doesn't need a DB, a pool, or a live server.
//
//	per-request override > originating channel instance > sole project
func resolveProject(ctx context.Context, exists func(string) bool, all []string) string {
	if ov := ProjectOverride(ctx); ov != "" && exists(ov) {
		return ov
	}
	if pid := ChannelProject(ctx); pid != "" {
		return pid
	}
	if len(all) == 1 {
		return all[0]
	}
	return ""
}

func TestProjectCtx_InstanceAndOverrideAreIndependent(t *testing.T) {
	base := context.Background()

	if got := ChannelProject(base); got != "" {
		t.Errorf("bare ctx channel project = %q, want empty", got)
	}
	if got := ProjectOverride(base); got != "" {
		t.Errorf("bare ctx override = %q, want empty", got)
	}

	// Both values ride the same ctx without clobbering each other — REST
	// stamps its instance project, then layers a per-request override.
	ctx := WithChannelProject(base, "proj-instance")
	ctx = WithProjectOverride(ctx, "proj-request")

	if got := ChannelProject(ctx); got != "proj-instance" {
		t.Errorf("channel project = %q, want proj-instance", got)
	}
	if got := ProjectOverride(ctx); got != "proj-request" {
		t.Errorf("override = %q, want proj-request", got)
	}

	// Empty values are no-ops, so an unconfigured instance never masks
	// a value set further up the chain.
	if got := ChannelProject(WithChannelProject(ctx, "")); got != "proj-instance" {
		t.Errorf("empty stamp overwrote existing project, got %q", got)
	}
}

func TestProjectCtx_ResolutionPrecedence(t *testing.T) {
	real := func(id string) bool { return id == "proj-request" || id == "proj-instance" }
	twoProjects := []string{"proj-a", "proj-b"}

	tests := []struct {
		name     string
		ctx      context.Context
		all      []string
		want     string
		wantWhy  string
		existsFn func(string) bool
	}{
		{
			name:    "override outranks instance",
			ctx:     WithProjectOverride(WithChannelProject(context.Background(), "proj-instance"), "proj-request"),
			all:     twoProjects,
			want:    "proj-request",
			wantWhy: "a REST body naming a project must win over the channel default",
		},
		{
			name:    "instance wins when no override",
			ctx:     WithChannelProject(context.Background(), "proj-instance"),
			all:     twoProjects,
			want:    "proj-instance",
			wantWhy: "this is the multi-bot case: each instance keeps its own project",
		},
		{
			name:    "unknown override falls back to instance",
			ctx:     WithProjectOverride(WithChannelProject(context.Background(), "proj-instance"), "ghost-project"),
			all:     twoProjects,
			want:    "proj-instance",
			wantWhy: "an override naming a deleted project must not strand the session",
		},
		{
			name:    "sole project used when nothing configured",
			ctx:     context.Background(),
			all:     []string{"proj-only"},
			want:    "proj-only",
			wantWhy: "single-project boxes keep working with no channel config",
		},
		{
			name:    "no project when several exist and none chosen",
			ctx:     context.Background(),
			all:     twoProjects,
			want:    "",
			wantWhy: "ambiguous: the pool falls back to the per-session temp cwd",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exists := real
			if tc.existsFn != nil {
				exists = tc.existsFn
			}
			if got := resolveProject(tc.ctx, exists, tc.all); got != tc.want {
				t.Errorf("resolved %q, want %q — %s", got, tc.want, tc.wantWhy)
			}
		})
	}
}
