package tags

import (
	"context"
	"testing"

	"github.com/yogasw/wick/pkg/tool"
)

// A row seeded under an old DefaultTags moves to the new tag once; after
// that (or after an admin removed the old tag) the swap leaves it alone.
func TestSwapToolTag(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newTagsSQLite(t))
	path := "/connectors/notif"
	if err := svc.EnsureToolDefaultTags(ctx, path, []tool.DefaultTag{Connector, Communication}); err != nil {
		t.Fatal(err)
	}
	names := func() map[string]bool {
		links, err := svc.repo.ListToolTags(ctx, []string{path})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, l := range links {
			ts, _ := svc.repo.TagsByIDs(ctx, []string{l.TagID})
			for _, tg := range ts {
				out[tg.Name] = true
			}
		}
		return out
	}
	for i := 0; i < 2; i++ {
		if err := svc.SwapToolTag(ctx, path, Communication.Name, Platform); err != nil {
			t.Fatal(err)
		}
		got := names()
		if !got["Platform"] || got["Communication"] || !got["Connector"] || len(got) != 2 {
			t.Fatalf("run %d: tags = %v", i, got)
		}
	}
	// Admin unlinks Platform afterwards: the swap does not bring it back.
	pt, _ := svc.repo.GetTagByName(ctx, "Platform")
	_ = svc.repo.UnlinkToolTag(ctx, path, pt.ID)
	if err := svc.SwapToolTag(ctx, path, Communication.Name, Platform); err != nil {
		t.Fatal(err)
	}
	if names()["Platform"] {
		t.Fatal("swap re-linked a tag the admin removed")
	}
}
