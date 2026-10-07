package manager

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/yogasw/wick/internal/entity"
)

// A job replaced by a plugin keeps its row (history) but is never listed
// or handed to the scheduler, so it cannot run next to its replacement.
func TestHiddenJobsAreNotListedOrScheduled(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.Job{}, &entity.JobRun{}); err != nil {
		t.Fatal(err)
	}
	for _, j := range []entity.Job{
		{Key: "notion-ticket-sync", Name: "a", Schedule: "* * * * *", Enabled: true},
		{Key: "notion_ticket_sync", Name: "b", Schedule: "* * * * *", Enabled: true},
	} {
		if err := db.Create(&j).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := NewServiceFromDB(db)
	s.SetHidden(func(k string) bool { return k == "notion-ticket-sync" })
	ctx := context.Background()
	for name, list := range map[string]func(context.Context) ([]entity.Job, error){"ListJobs": s.ListJobs, "ListEnabledJobs": s.ListEnabledJobs} {
		js, err := list(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(js) != 1 || js[0].Key != "notion_ticket_sync" {
			t.Fatalf("%s = %+v", name, js)
		}
	}
}
