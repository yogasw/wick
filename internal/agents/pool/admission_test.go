package pool

import "testing"

// Below the floor, a spawn queues instead of starting. This is the layer
// that prevents the one start that pushes the machine over the edge.
func TestMemoryAdmits(t *testing.T) {
	cases := []struct {
		name       string
		minFreeMB  int
		availBytes uint64
		availKnown bool
		want       bool
	}{
		{"plenty free admits", 512, 2 * 1024 * 1024 * 1024, true, true},
		{"below floor refuses", 512, 100 * 1024 * 1024, true, false},
		{"exactly at floor admits", 512, 512 * 1024 * 1024, true, true},
		{"one byte under refuses", 512, 512*1024*1024 - 1, true, false},
		{"zero floor disables the check", 0, 1024, true, true},
		{"negative floor disables the check", -1, 1024, true, true},
		// Unknown availability must not become a silent spawn ban: the
		// guard is advisory, and refusing every spawn would be a worse
		// failure than the one it prevents.
		{"unknown availability admits", 512, 0, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := memoryAdmits(c.minFreeMB, c.availBytes, c.availKnown)
			if got != c.want {
				t.Fatalf("memoryAdmits(%d, %d, %v) = %v, want %v",
					c.minFreeMB, c.availBytes, c.availKnown, got, c.want)
			}
		})
	}
}
