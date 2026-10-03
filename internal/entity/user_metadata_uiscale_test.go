package entity

import "testing"

func TestValidUIScale(t *testing.T) {
	for in, want := range map[int]int{80: 80, 85: 85, 90: 90, 100: 100, 0: 90, 75: 90, 105: 90, 87: 90, -5: 90} {
		if got := ValidUIScale(in); got != want {
			t.Errorf("ValidUIScale(%d) = %d, want %d", in, got, want)
		}
	}
	if (UserMetadata{}).UIScaleOrDefault() != UIScaleDefault || (UserMetadata{UIScale: 95}).UIScaleOrDefault() != 95 {
		t.Fatal("UIScaleOrDefault")
	}
}
