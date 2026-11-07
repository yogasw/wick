package main

import (
	"testing"
	"time"
)

func TestBehindSchedule(t *testing.T) {
	start := time.Now()
	if behindSchedule(start, 5) {
		t.Fatal("a path that just started is not behind")
	}
	late := time.Now().Add(-500 * time.Millisecond)
	if !behindSchedule(late, 5) {
		t.Fatal("point 5 is due at ~96ms, 500ms in is behind")
	}
	if behindSchedule(late, 40) {
		t.Fatal("point 40 is due at ~656ms, 500ms in is not behind yet")
	}
}
