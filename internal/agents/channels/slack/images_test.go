package slack

import (
	"strings"
	"testing"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/imagecard"
)

func TestImageBlocks(t *testing.T) {
	blocks := imageBlocks([]imagecard.Image{
		{URL: "https://example.com/a.gif", Caption: "cat"},
		{URL: "https://example.com/b.gif", Caption: strings.Repeat("x", 2100)},
		{URL: "https://example.com/c.gif"},
	})
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks", len(blocks))
	}
	a := blocks[0].(*slackgo.ImageBlock)
	if a.ImageURL != "https://example.com/a.gif" || a.AltText != "cat" || a.Title == nil || a.Title.Text != "cat" {
		t.Fatalf("block 0 = %+v", a)
	}
	b := blocks[1].(*slackgo.ImageBlock)
	if len([]rune(b.AltText)) != 2000 || len([]rune(b.Title.Text)) != 2000 {
		t.Fatalf("caption not clipped: alt %d title %d", len(b.AltText), len(b.Title.Text))
	}
	c := blocks[2].(*slackgo.ImageBlock)
	if c.AltText != "image" || c.Title != nil {
		t.Fatalf("block 2 = %+v", c)
	}
}
