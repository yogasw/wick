package entity

import "testing"

func TestValidUIScale(t *testing.T) {
	for in, want := range map[int]int{80: 80, 85: 85, 90: 90, 100: 100, 150: 150, 0: 100, 75: 100, 155: 100, 87: 100, -5: 100} {
		if got := ValidUIScale(in); got != want {
			t.Errorf("ValidUIScale(%d) = %d, want %d", in, got, want)
		}
	}
	if (UserMetadata{}).UIScaleOrDefault() != UIScaleDefault || (UserMetadata{UIScale: 95}).UIScaleOrDefault() != 95 {
		t.Fatal("UIScaleOrDefault")
	}
}
