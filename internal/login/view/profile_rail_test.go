package view

import (
	"bytes"
	"context"
	"regexp"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// The rail style choice reads back from the profile: unset is Icons, and a
// stored Labels checks the other option.
func TestProfileRailStyleRadio(t *testing.T) {
	checked := func(u *entity.User) string {
		var b bytes.Buffer
		if err := ProfilePage(u, "", false, false, 8).Render(context.Background(), &b); err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(`<input[^>]*name="rail_labels"[^>]*>`).FindAllString(b.String(), -1)
		if len(m) != 2 {
			t.Fatalf("want 2 rail_labels radios, got %d", len(m))
		}
		for _, in := range m {
			if regexp.MustCompile(`\schecked[\s>/]`).MatchString(in) {
				return regexp.MustCompile(`value="([^"]+)"`).FindStringSubmatch(in)[1]
			}
		}
		return ""
	}
	if got := checked(&entity.User{}); got != entity.RailStyleIcons {
		t.Fatalf("unset: checked %q, want %q", got, entity.RailStyleIcons)
	}
	u := &entity.User{}
	u.Metadata.Rail.Labels = true
	if got := checked(u); got != entity.RailStyleLabels {
		t.Fatalf("labels on: checked %q, want %q", got, entity.RailStyleLabels)
	}
}
