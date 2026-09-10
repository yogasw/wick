package agents

import "testing"

// The bug this guards: the Git CLI connector reports a policy refusal as
// a NORMAL reply — ok:false with verdict "deny" — not as a transport
// error. Reading only the transport error made the panel announce
// "Pulled" while git never ran and the branch stayed 65 commits behind.
func TestGitConnectorOutput(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{
			name: "policy denial is an error, with its reason",
			raw: `{"ok":false,"exit_code":-1,"stdout":"","stderr":"",
				"policy":{"verdict":"deny","reason":"branch \"master\" is protected; direct pull is blocked"}}`,
			wantErr: `branch "master" is protected; direct pull is blocked`,
		},
		{
			name:    "denial with no reason still fails",
			raw:     `{"ok":false,"policy":{"verdict":"deny"}}`,
			wantErr: "refused by the connector's policy",
		},
		{
			name:    "git failure surfaces stderr",
			raw:     `{"ok":false,"exit_code":128,"stderr":"fatal: could not read Username"}`,
			wantErr: "fatal: could not read Username",
		},
		{
			name:    "git failure with no output names the exit code",
			raw:     `{"ok":false,"exit_code":128}`,
			wantErr: "git exited 128",
		},
		{
			name: "success returns the terminal output",
			raw:  `{"ok":true,"exit_code":0,"stdout":"Updating 6bd4303..9f1c2ab\n","policy":{"verdict":"allow"}}`,
			want: "Updating 6bd4303..9f1c2ab",
		},
		{
			name: "silent success still reports something",
			raw:  `{"ok":true,"exit_code":0,"policy":{"verdict":"allow"}}`,
			want: "done",
		},
		{
			name: "unparseable payload is passed through rather than swallowed",
			raw:  "not json",
			want: "not json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gitConnectorOutput(tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want error %q, got output %q", tt.wantErr, got)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("error = %q, want %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

// The choice is filed per user, per session — never per repo. Switching
// repos must not re-ask, and one person's credential must not become
// another's in a shared session.
func TestGitConnKeyIsPerUserNotPerRepo(t *testing.T) {
	if gitConnKey("user-a") == gitConnKey("user-b") {
		t.Fatal("two users share one key — a shared session would inherit a credential")
	}
	if gitConnKey("") == gitConnKey("user-a") {
		t.Fatal("the App Owner shares a key with a named user")
	}
}
