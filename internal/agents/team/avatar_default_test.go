package team

import (
	"reflect"
	"testing"
)

// The vectors match fe/common/avatar/src/__tests__/shape.test.ts, so the
// server and the UI pick the same default for the same handle.
func TestDefaultAvatarForMatchesUI(t *testing.T) {
	cases := map[string]Avatar{
		"log-hunter": {Shape: "circle", Color: "#0ea5e9"},
		"captain":    {Shape: "squircle", Color: "#f59e0b"},
	}
	for h, want := range cases {
		if got := DefaultAvatarFor(h); !reflect.DeepEqual(got, want) {
			t.Errorf("DefaultAvatarFor(%q) = %+v, want %+v", h, got, want)
		}
	}
	if !reflect.DeepEqual(DefaultAvatarFor("x-1"), DefaultAvatarFor("x-1")) {
		t.Error("not deterministic")
	}
	if !reflect.DeepEqual(NormalizeAvatar(DefaultAvatarFor("anything")), DefaultAvatarFor("anything")) {
		t.Error("default must survive NormalizeAvatar")
	}
}
