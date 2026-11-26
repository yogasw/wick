package datatable

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/yogasw/wick/internal/entity"
	"gorm.io/gorm"
)

func openChangeFeedTest(t *testing.T,path string) *gorm.DB {
	t.Helper()
	db,err := gorm.Open(sqlite.Open(path),&gorm.Config{})
	if err != nil { t.Fatal(err) }
	sqlDB,err := db.DB()
	if err != nil { t.Fatal(err) }
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func(){ _ = sqlDB.Close() })
	if err := db.AutoMigrate(&entity.DataTable{},&entity.DataTableRow{}); err != nil { t.Fatal(err) }
	return db
}

func TestChangeFeedTransactionsAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(),"feed.db")
	db := openChangeFeedTest(t,path)
	f,err := InstallChangeFeed(db)
	if err != nil { t.Fatal(err) }
	s := NewPg(db)
	if err := f.SetEnabled("selected",true); err != nil { t.Fatal(err) }
	for _, slug := range []string{"selected","private"} {
		if err := s.CreateTable(Schema{Slug:slug,Mode:ModeLax}); err != nil { t.Fatal(err) }
		if err := s.Insert(slug,map[string]any{}); err != nil { t.Fatal(err) }
	}
	if _,err := s.Update("selected",map[string]any{ColID:int64(1)}); err != nil { t.Fatal(err) }
	if n,err := s.Delete("selected",map[string]any{ColID:int64(1)}); err != nil || n != 1 { t.Fatalf("delete: %d %v",n,err) }
	rollback := errors.New("rollback")
	if err := db.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Create(&entity.DataTableRow{TableSlug:"selected",ID:99,Data:"{}",CreatedAt:now,UpdatedAt:now}).Error; err != nil { return err }
		return rollback
	}); !errors.Is(err,rollback) { t.Fatalf("rollback: %v",err) }
	if err := s.Insert("selected",map[string]any{}); err != nil { t.Fatal(err) }
	events,err := f.Read("selected",0,100)
	if err != nil { t.Fatal(err) }
	want := []string{"table_insert","row_insert","row_update","row_delete","row_insert"}
	if len(events) != len(want) { t.Fatalf("events: %+v",events) }
	for i,e := range events {
		if e.Sequence != int64(i+1) || e.Version != 1 || e.Kind != want[i] { t.Fatalf("event %d: %+v",i,e) }
		if i == 0 && e.RowID != nil { t.Fatal("schema event has row ID") }
		if i > 0 && (e.RowID == nil || *e.RowID == 99) { t.Fatalf("invalid row ID: %+v",e) }
	}
	if rows,err := f.Read("private",0,100); err != nil || len(rows) != 0 { t.Fatalf("private: %+v %v",rows,err) }
	if err := f.SetEnabled("selected",false); err != nil { t.Fatal(err) }
	if err := s.Insert("selected",map[string]any{}); err != nil { t.Fatal(err) }
	if err := f.SetEnabled("selected",true); err != nil { t.Fatal(err) }
	sqlDB,_ := db.DB()
	if err := sqlDB.Close(); err != nil { t.Fatal(err) }
	db = openChangeFeedTest(t,path)
	f,err = InstallChangeFeed(db)
	if err != nil { t.Fatal(err) }
	if err := NewPg(db).DropTable("selected"); err != nil { t.Fatal(err) }
	page,err := f.Read("selected",5,1)
	if err != nil || len(page) != 1 || page[0].Sequence != 6 || page[0].Kind != "row_delete" { t.Fatalf("resume: %+v %v",page,err) }
	tail,err := f.Read("selected",6,100)
	if err != nil || len(tail) != 2 || tail[1].Kind != "table_delete" { t.Fatalf("tail: %+v %v",tail,err) }
	if _,err := f.Read("selected",0,1001); err == nil { t.Fatal("unbounded page accepted") }
}

func TestChangeFeedFailureRollsBackActualWrite(t *testing.T) {
	db := openChangeFeedTest(t,filepath.Join(t.TempDir(),"failure.db"))
	f,err := InstallChangeFeed(db)
	if err != nil { t.Fatal(err) }
	s := NewPg(db)
	if err := f.SetEnabled("selected",true); err != nil { t.Fatal(err) }
	if err := s.CreateTable(Schema{Slug:"selected",Mode:ModeLax}); err != nil { t.Fatal(err) }
	if err := db.Exec("DROP TABLE wick_dt_change_events").Error; err != nil { t.Fatal(err) }
	if err := s.Insert("selected",map[string]any{}); err == nil { t.Fatal("write committed without outbox") }
	var count int64
	if err := db.Model(&entity.DataTableRow{}).Count(&count).Error; err != nil || count != 0 { t.Fatalf("rows: %d %v",count,err) }
	var scope ChangeScope
	if err := db.First(&scope,"slug = ?","selected").Error; err != nil || scope.Sequence != 1 { t.Fatalf("scope: %+v %v",scope,err) }
}
