package admin

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestRepo(t *testing.T) *repo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.Tag{}, &entity.ToolTag{}, &entity.UserTag{}); err != nil {
		t.Fatal(err)
	}
	return newRepo(db)
}

func TestUpdateTagRejectsOwnerTag(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	tag := &entity.Tag{Name: "owner:some-uuid", IsFilter: true}
	if err := r.db.WithContext(ctx).Create(tag).Error; err != nil {
		t.Fatal(err)
	}

	err := r.UpdateTag(ctx, tag.ID, "new-name", "", false, false, 0)
	if err != ErrOwnerTagImmutable {
		t.Fatalf("expected ErrOwnerTagImmutable, got %v", err)
	}
}

func TestDeleteTagRejectsOwnerTag(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	tag := &entity.Tag{Name: "owner:some-uuid", IsFilter: true}
	if err := r.db.WithContext(ctx).Create(tag).Error; err != nil {
		t.Fatal(err)
	}

	err := r.DeleteTag(ctx, tag.ID)
	if err != ErrOwnerTagImmutable {
		t.Fatalf("expected ErrOwnerTagImmutable, got %v", err)
	}
}

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
