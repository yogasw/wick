package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/ui"
)

func renderJobPage(t *testing.T, embedded bool) string {
	t.Helper()
	j := &entity.Job{Key: "daily-recap", Name: "Daily Recap", Description: "Post yesterday's summary", Icon: "@", Enabled: true}
	var buf bytes.Buffer
	ctx := ui.WithEmbedded(context.Background(), embedded)
	if err := JobPage(j, nil, nil, "", nil).Render(ctx, &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestJobPageDropsNavbarInsideAFrame(t *testing.T) {
	full := renderJobPage(t, false)
	if !strings.Contains(full, "<nav") {
		t.Fatal("normal job page rendered without the navbar")
	}

	framed := renderJobPage(t, true)
	if strings.Contains(framed, "<nav") {
		t.Fatal("navbar still rendered inside a frame")
	}
	// The job's own title strip and Run Now button are the page, not
	// chrome — embed mode must keep them.
	if !strings.Contains(framed, "Daily Recap") || !strings.Contains(framed, "Run Now") {
		t.Fatal("embed mode stripped the job's own controls")
	}
}
