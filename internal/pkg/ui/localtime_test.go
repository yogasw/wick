package ui

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLocalTimeRendersInstantAndServerFallback(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	ts := time.Date(2026, 9, 2, 15, 52, 20, 0, loc)
	var sb strings.Builder
	if err := LocalTime(ts, TimeFormatISO).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := sb.String()
	for _, want := range []string{`datetime="2026-09-02T15:52:20+07:00"`, `data-local="iso"`, `2026-09-02 15:52`} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered %q, missing %q", got, want)
		}
	}
}
