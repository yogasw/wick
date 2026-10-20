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
