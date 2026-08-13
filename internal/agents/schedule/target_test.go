package schedule

import (
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

func TestResolveTarget_Modes(t *testing.T) {
	firedAt := time.Date(2026, 8, 13, 9, 5, 0, 0, time.UTC)

	tests := []struct {
		name     string
		row      entity.ScheduledMessage
		want     string
		wantMint bool
		wantErr  string
	}{
		{
			name: "existing delivers into the fixed session",
			row:  entity.ScheduledMessage{SessionID: "sess-1"},
			want: "sess-1", wantMint: false,
		},
		{
			// Legacy rows written before session_mode existed carry "" and
			// must keep behaving as session-scoped.
			name: "empty mode is treated as existing",
			row:  entity.ScheduledMessage{SessionID: "sess-1", SessionMode: ""},
			want: "sess-1", wantMint: false,
		},
		{
			name:    "existing without a session is an error",
			row:     entity.ScheduledMessage{SessionMode: entity.ScheduledSessionExisting},
			wantErr: "no target session",
		},
		{
			name: "new generates a per-fire id from the run count",
			row: entity.ScheduledMessage{
				ID: "sm_a1b2c3d4-1111-2222-3333-444455556666", ProjectID: "p1",
				SessionMode: entity.ScheduledSessionNew, RunCount: 3,
			},
			want: "sch-a1b2c3d4-3", wantMint: true,
		},
		{
			name: "new requires a project",
			row: entity.ScheduledMessage{
				ID: "sm_a1b2c3d4", SessionMode: entity.ScheduledSessionNew,
			},
			wantErr: "requires a project",
		},
		{
			name: "template renders the fire date",
			row: entity.ScheduledMessage{
				ID: "sm_a1b2c3d4", ProjectID: "p1",
				SessionMode: entity.ScheduledSessionTemplate, SessionTemplate: "daily-{date}",
			},
			want: "daily-2026-08-13", wantMint: true,
		},
		{
			// The degenerate template — no placeholder — is how "always reuse
			// this one session" is expressed.
			name: "template with no placeholder is a fixed session",
			row: entity.ScheduledMessage{
				ID: "sm_a1b2c3d4", ProjectID: "p1",
				SessionMode: entity.ScheduledSessionTemplate, SessionTemplate: "nightly-build",
			},
			want: "nightly-build", wantMint: true,
		},
		{
			name: "template requires a project",
			row: entity.ScheduledMessage{
				ID: "sm_x", SessionMode: entity.ScheduledSessionTemplate, SessionTemplate: "daily-{date}",
			},
			wantErr: "requires a project",
		},
		{
			name: "unknown mode is an error",
			row: entity.ScheduledMessage{
				SessionID: "s", ProjectID: "p1", SessionMode: "sideways",
			},
			wantErr: "unknown session_mode",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, mint, err := ResolveTarget(tc.row, firedAt)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("session = %q, want %q", got, tc.want)
			}
			if mint != tc.wantMint {
				t.Fatalf("mint = %v, want %v", mint, tc.wantMint)
			}
		})
	}
}

// Mode "new" must give a different session every fire — that is the whole
// point of the mode, and it must hold without any extra stored state beyond
// the run counter the claim already bumps.
func TestResolveTarget_NewIsUniquePerFire(t *testing.T) {
	firedAt := time.Now()
	row := entity.ScheduledMessage{
		ID: "sm_deadbeef-0000", ProjectID: "p1", SessionMode: entity.ScheduledSessionNew,
	}
	seen := map[string]bool{}
	for run := 1; run <= 5; run++ {
		row.RunCount = run
		got, _, err := ResolveTarget(row, firedAt)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if seen[got] {
			t.Fatalf("run %d reused session id %q", run, got)
		}
		seen[got] = true
	}
}

// Mode "template" is the opposite guarantee: fires that render the same id
// share the session, and only a time-varying token splits them.
func TestResolveTarget_TemplateGroupsByToken(t *testing.T) {
	row := entity.ScheduledMessage{
		ID: "sm_deadbeef", ProjectID: "p1",
		SessionMode: entity.ScheduledSessionTemplate, SessionTemplate: "daily-{date}",
	}
	morning, _, err := ResolveTarget(row, time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	evening, _, err := ResolveTarget(row, time.Date(2026, 8, 13, 23, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if morning != evening {
		t.Fatalf("same day rendered differently: %q vs %q", morning, evening)
	}
	tomorrow, _, err := ResolveTarget(row, time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if tomorrow == morning {
		t.Fatalf("next day reused %q", morning)
	}
}

func TestRenderTemplate(t *testing.T) {
	row := entity.ScheduledMessage{ID: "sm_a1b2c3d4-ffff", RunCount: 7}
	firedAt := time.Date(2026, 8, 13, 9, 5, 0, 0, time.UTC)

	tests := []struct {
		tpl     string
		want    string
		wantErr string
	}{
		{tpl: "r-{date}", want: "r-2026-08-13"},
		{tpl: "r-{datetime}", want: "r-2026-08-13-0905"},
		{tpl: "r-{ym}", want: "r-2026-08"},
		{tpl: "r-{run}", want: "r-7"},
		{tpl: "r-{id}", want: "r-a1b2c3d4"},
		{tpl: "{id}-{ym}-{run}", want: "a1b2c3d4-2026-08-7"},
		{tpl: "", wantErr: "session_template is required"},
		{tpl: "r-{nope}", wantErr: "unknown placeholder"},
		// A rendered id must still be a legal session id — the pattern is
		// user input and ends up as a directory name.
		{tpl: "bad/slash-{date}", wantErr: "invalid session id"},
		{tpl: "../escape", wantErr: "invalid session id"},
	}
	for _, tc := range tests {
		t.Run(tc.tpl, func(t *testing.T) {
			got, err := RenderTemplate(tc.tpl, row, firedAt)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("render = %q, want %q", got, tc.want)
			}
		})
	}
}

// A template is rendered in UTC so a schedule's ids don't shift with the
// server's local zone.
func TestRenderTemplate_UsesUTC(t *testing.T) {
	zone := time.FixedZone("UTC+9", 9*3600)
	// 2026-08-13 06:00 UTC is already the 13th locally and the 13th in UTC;
	// 2026-08-13 21:00 UTC is the 14th in UTC+9 but must still render the 13th.
	got, err := RenderTemplate("d-{date}", entity.ScheduledMessage{ID: "sm_x"},
		time.Date(2026, 8, 13, 21, 0, 0, 0, time.UTC).In(zone))
	if err != nil {
		t.Fatal(err)
	}
	if got != "d-2026-08-13" {
		t.Fatalf("render = %q, want d-2026-08-13 (UTC)", got)
	}
}

func TestNormalizeTargetSpec(t *testing.T) {
	tests := []struct {
		name string
		in   TargetSpec
		want string
	}{
		{
			// The one-arg ergonomic case: naming only a project means "run it
			// there, fresh session each time".
			name: "project only implies new",
			in:   TargetSpec{ProjectID: "p1"},
			want: entity.ScheduledSessionNew,
		},
		{
			name: "session only implies existing",
			in:   TargetSpec{SessionID: "s1"},
			want: entity.ScheduledSessionExisting,
		},
		{
			name: "a template implies template mode",
			in:   TargetSpec{ProjectID: "p1", Template: "daily-{date}"},
			want: entity.ScheduledSessionTemplate,
		},
		{
			name: "an explicit mode always wins",
			in:   TargetSpec{ProjectID: "p1", Template: "daily-{date}", Mode: entity.ScheduledSessionNew},
			want: entity.ScheduledSessionNew,
		},
		{
			name: "nothing named falls back to existing",
			in:   TargetSpec{},
			want: entity.ScheduledSessionExisting,
		},
		{
			// session_id wins when both are named: the session's own project
			// governs the cwd at fire time, so the project arg is redundant.
			name: "both named keeps session scope",
			in:   TargetSpec{SessionID: "s1", ProjectID: "p1"},
			want: entity.ScheduledSessionExisting,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeTargetSpec(tc.in).Mode; got != tc.want {
				t.Fatalf("mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeTargetSpec_TrimsWhitespace(t *testing.T) {
	got := NormalizeTargetSpec(TargetSpec{ProjectID: "  p1  ", Template: " daily-{date} "})
	if got.ProjectID != "p1" || got.Template != "daily-{date}" {
		t.Fatalf("not trimmed: %+v", got)
	}
}

func TestValidateTargetSpec(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name    string
		in      TargetSpec
		wantErr string
	}{
		{name: "existing with a session is ok", in: TargetSpec{Mode: entity.ScheduledSessionExisting, SessionID: "s1"}},
		{name: "existing without a session", in: TargetSpec{Mode: entity.ScheduledSessionExisting}, wantErr: "session_id is required"},
		{name: "new with a project is ok", in: TargetSpec{Mode: entity.ScheduledSessionNew, ProjectID: "p1"}},
		{name: "new without a project", in: TargetSpec{Mode: entity.ScheduledSessionNew}, wantErr: "project_id is required"},
		{name: "template with a project and pattern is ok", in: TargetSpec{Mode: entity.ScheduledSessionTemplate, ProjectID: "p1", Template: "d-{date}"}},
		{name: "template without a project", in: TargetSpec{Mode: entity.ScheduledSessionTemplate, Template: "d-{date}"}, wantErr: "project_id is required"},
		{name: "template without a pattern", in: TargetSpec{Mode: entity.ScheduledSessionTemplate, ProjectID: "p1"}, wantErr: "session_template is required"},
		{
			// The point of validating at create: a pattern that can never
			// render a usable id fails now, not on fire 40 at 3am.
			name: "template that cannot render a legal id", in: TargetSpec{Mode: entity.ScheduledSessionTemplate, ProjectID: "p1", Template: "bad/{date}"},
			wantErr: "invalid session id",
		},
		{name: "unknown mode", in: TargetSpec{Mode: "sideways", ProjectID: "p1"}, wantErr: "session_mode must be one of"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTargetSpec(tc.in, now)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestShortScheduleID(t *testing.T) {
	tests := []struct{ in, want string }{
		{"sm_a1b2c3d4-1111-2222", "a1b2c3d4"},
		{"sm_ab", "ab"},
		{"", "anon"},
		{"sm_", "anon"},
		// Dashes are stripped before truncating so a short uuid segment
		// doesn't leak a leading/trailing dash into the session id.
		{"sm_a-b-c-d-e-f-g-h-i", "abcdefgh"},
	}
	for _, tc := range tests {
		if got := shortScheduleID(tc.in); got != tc.want {
			t.Fatalf("shortScheduleID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
