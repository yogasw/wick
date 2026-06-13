package admin

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestFilterOutOwnerTags(t *testing.T) {
	tags := []*entity.Tag{
		{ID: "1", Name: "engineering"},
		{ID: "2", Name: "owner:abc-123"},
		{ID: "3", Name: "design"},
		{ID: "4", Name: "owner:def-456"},
	}
	got := filterOutOwnerTags(tags)
	if len(got) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(got))
	}
	for _, tag := range got {
		if len(tag.Name) > 6 && tag.Name[:6] == "owner:" {
			t.Error("owner: tag leaked through filter:", tag.Name)
		}
	}
}
