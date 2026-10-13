package team

import "testing"

// "Show teammates in channels": unset (an agent saved before the field)
// reads on.
func TestShowsTeammatesInChannels(t *testing.T) {
	on, off := true, false
	for _, c := range []struct {
		set  *bool
		want bool
	}{{nil, true}, {&on, true}, {&off, false}} {
		if got := ShowsTeammatesInChannels(c.set); got != c.want {
			t.Errorf("ShowsTeammatesInChannels(%v) = %v, want %v", c.set, got, c.want)
		}
	}
}

// "Hide in-between messages in channels": unset reads on.
func TestHidesInterimInChannels(t *testing.T) {
	on, off := true, false
	for _, c := range []struct {
		set  *bool
		want bool
	}{{nil, true}, {&on, true}, {&off, false}} {
		if got := HidesInterimInChannels(c.set); got != c.want {
			t.Errorf("HidesInterimInChannels(%v) = %v, want %v", c.set, got, c.want)
		}
	}
}
