package datatable

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestChangeCursorRestartAndRebootstrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cursor.db")
	db := openChangeFeedTest(t,path)
	runChangeCursorContract(t,db,"cursor-scope")
	sqlDB,_ := db.DB()
	if err := sqlDB.Close(); err != nil { t.Fatal(err) }
	db = openChangeFeedTest(t,path)
	f,err := InstallChangeFeed(db)
	if err != nil { t.Fatal(err) }
	after,events,err := f.ReadForPeer("cursor-scope",strings.Repeat("a",64),10)
	if err != nil || after != 5 || len(events)!=0 { t.Fatalf("durable cursor: %d %+v %v",after,events,err) }
}

func runChangeCursorContract(t *testing.T,db *gorm.DB,slug string) {
	t.Helper()
	a,b := strings.Repeat("a",64),strings.Repeat("b",64)
	f,err := InstallChangeFeed(db)
	if err != nil { t.Fatal(err) }
	s := NewPg(db)
	if err := f.SetEnabled(slug,true); err != nil { t.Fatal(err) }
	if err := s.CreateTable(Schema{Slug:slug,Mode:ModeStrict}); err != nil { t.Fatal(err) }
	if _,_,err := f.ReadForPeer(slug,a,10); !errors.Is(err,ErrChangeSnapshotRequired) { t.Fatal(err) }
	if err := f.ConfirmSnapshot(slug,a,0); !errors.Is(err,ErrChangeSnapshotRequired) { t.Fatal(err) }
	if err := f.ConfirmSnapshot(slug,a,2); err != nil { t.Fatal(err) }
	if err := s.Insert(slug,map[string]any{}); err != nil { t.Fatal(err) }
	after,events,err := f.ReadForPeer(slug,a,10)
	if err != nil || after != 2 || len(events)!=1 || events[0].Sequence!=3 { t.Fatalf("page: %d %+v %v",after,events,err) }
	if err := f.AcknowledgePage(slug,a,2,4); !errors.Is(err,ErrChangeCursorConflict) { t.Fatal(err) }
	if err := f.AcknowledgePage(slug,a,2,3); err != nil { t.Fatal(err) }
	if err := f.AcknowledgePage(slug,a,2,3); err != nil { t.Fatal(err) }
	if _,_,err := f.ReadForPeer(slug,b,10); !errors.Is(err,ErrChangeSnapshotRequired) { t.Fatal("peer isolation",err) }
	if err := f.SetEnabled(slug,false); err != nil { t.Fatal(err) }
	if _,_,err := f.ReadForPeer(slug,a,10); !errors.Is(err,ErrChangeFeedDisabled) { t.Fatal(err) }
	if err := s.Insert(slug,map[string]any{}); err != nil { t.Fatal(err) }
	if err := f.SetEnabled(slug,true); err != nil { t.Fatal(err) }
	if _,_,err := f.ReadForPeer(slug,a,10); !errors.Is(err,ErrChangeSnapshotRequired) { t.Fatal(err) }
	if err := f.ConfirmSnapshot(slug,a,3); !errors.Is(err,ErrChangeSnapshotRequired) { t.Fatal(err) }
	if err := f.ConfirmSnapshot(slug,a,4); err != nil { t.Fatal(err) }
	if err := s.Insert(slug,map[string]any{}); err != nil { t.Fatal(err) }
	if err := f.AcknowledgePage(slug,a,4,5); err != nil { t.Fatal(err) }
	if err := f.ConfirmSnapshot(slug,a,4); !errors.Is(err,ErrChangeCursorConflict) { t.Fatal(err) }
	if err := f.ConfirmSnapshot(slug,"bad-peer",5); err == nil { t.Fatal("invalid peer accepted") }
}
