package gate

import (
	"os"
	"reflect"
	"testing"
)

func TestSessionRoleRoundTrip(t *testing.T) {
	t.Setenv("WICK_DATA_DIR", t.TempDir())
	const app, sid = "wick-test", "sess-1"

	if _, ok, err := LoadSessionRole(app, sid); ok || err != nil {
		t.Fatalf("no file yet: ok=%v err=%v", ok, err)
	}
	want := SessionRole{Role: "guest", Deny: []string{"Bash", "Write"}}
	if err := WriteSessionRole(app, sid, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadSessionRole(app, sid)
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	if !got.Denies("bash") || got.Denies("WebSearch") {
		t.Fatalf("Denies wrong for %+v", got)
	}
	// Nothing denied removes the file.
	if err := WriteSessionRole(app, sid, SessionRole{Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := LoadSessionRole(app, sid); ok {
		t.Fatal("an empty deny list must remove the file")
	}
}

func TestSessionRoleUnreadableIsAnError(t *testing.T) {
	t.Setenv("WICK_DATA_DIR", t.TempDir())
	const app, sid = "wick-test", "sess-2"
	if err := WriteSessionRole(app, sid, SessionRole{Role: "guest", Deny: []string{"Bash"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SessionRolePath(app, sid), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadSessionRole(app, sid); err == nil {
		t.Fatal("a broken file must be an error, never no limit")
	}
}

func TestSessionRolePathUsesBaseName(t *testing.T) {
	t.Setenv("WICK_DATA_DIR", t.TempDir())
	if a, b := SessionRolePath("wick-test", "nested/sess-3"), SessionRolePath("wick-test", "sess-3"); a != b {
		t.Fatalf("a session id must not pick a directory: %s vs %s", a, b)
	}
}

func TestMergeDeny(t *testing.T) {
	got := MergeDeny([]string{"Bash", "Write"}, []string{"Write", "Edit"})
	if !reflect.DeepEqual(got, []string{"Bash", "Write", "Edit"}) {
		t.Fatalf("got %v", got)
	}
}
