package slack

import "testing"

func TestReactOnlyReply(t *testing.T) {
	for in, want := range map[string]string{
		"[react :pray:]": "pray", "[react:thumbsup]": "thumbsup", " [REACT :+1:] ": "+1",
		"[react :raised_hands::skin-tone-3:]": "raised_hands::skin-tone-3",
	} {
		if got, ok := reactOnlyReply(in); !ok || got != want {
			t.Errorf("%q = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"thanks [react :pray:]", "[react :pray:] sama-sama", "[react]", ""} {
		if _, ok := reactOnlyReply(in); ok {
			t.Errorf("%q must not be a react reply", in)
		}
	}
	if !reactPending("[rea") || !reactPending("[react :pr") || reactPending("Oke, sudah") {
		t.Fatal("reactPending must hold only what can still become a marker")
	}
}

func TestReactMarkerOpeningLine(t *testing.T) {
	name, ts, rest, ok := reactMarker("[react :ok_hand:]\nNoted, the next step is mine.\nSecond line.")
	if !ok || name != "ok_hand" || ts != "" || rest != "Noted, the next step is mine.\nSecond line." {
		t.Fatalf("got %q %q %q %v", name, ts, rest, ok)
	}
	name, ts, rest, ok = reactMarker("[react :+1: 1700000000.000100]")
	if !ok || name != "+1" || ts != "1700000000.000100" || rest != "" {
		t.Fatalf("explicit ts: got %q %q %q %v", name, ts, rest, ok)
	}
	if _, ok := reactOnlyReply("[react :+1: 1700000000.000100]\ndone"); ok {
		t.Fatal("a marker followed by text is not a react-only reply")
	}
	for _, in := range []string{"Done.\n[react :pray:]", "[react :pray:] thanks", "[react :pray: abc]"} {
		if _, _, _, ok := reactMarker(in); ok {
			t.Errorf("%q must not open with a react marker", in)
		}
	}
	if got := dropReactLine("[react :pray:]\nSiap."); got != "Siap." {
		t.Errorf("dropReactLine = %q", got)
	}
	if got := dropReactLine("Siap."); got != "Siap." {
		t.Errorf("dropReactLine left text = %q", got)
	}
}
