package datatable

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestChangeSnapshotSQLite(t *testing.T) {
	runChangeSnapshotContract(t,openChangeFeedTest(t,filepath.Join(t.TempDir(),"snapshot.db")),"snapshot-sqlite")
}

func runChangeSnapshotContract(t *testing.T,db *gorm.DB,slug string) {
	t.Helper()
	f,err := InstallChangeFeed(db)
	if err != nil { t.Fatal(err) }
	s := NewPg(db)
	ctx := context.Background()
	if _,err := f.Snapshot(ctx,slug,10,MaxSnapshotBytes); !errors.Is(err,ErrChangeFeedDisabled) { t.Fatal(err) }
	if err := s.CreateTable(Schema{Slug:slug,Mode:ModeStrict,Columns:[]Column{{Name:"title",Type:"string"}}}); err != nil { t.Fatal(err) }
	if err := s.Insert(slug,map[string]any{"title":"before activation"}); err != nil { t.Fatal(err) }
	if err := f.SetEnabled(slug,true); err != nil { t.Fatal(err) }
	if err := s.Insert(slug,map[string]any{"title":"after activation"}); err != nil { t.Fatal(err) }
	snap,err := f.Snapshot(ctx,slug,10,MaxSnapshotBytes)
	if err != nil || snap.Table==nil || snap.Watermark!=2 || len(snap.Rows)!=2 { t.Fatalf("snapshot: %+v %v",snap,err) }
	if snap.Rows[0].ID!=1 || !strings.Contains(snap.Rows[0].Data,"before activation") || snap.Rows[1].ID!=2 { t.Fatalf("rows: %+v",snap.Rows) }
	if _,err := f.Snapshot(ctx,slug,1,MaxSnapshotBytes); !errors.Is(err,ErrChangeSnapshotTooLarge) { t.Fatal(err) }
	if _,err := f.Snapshot(ctx,slug,10,32); !errors.Is(err,ErrChangeSnapshotTooLarge) { t.Fatal(err) }
	if err := s.Insert(slug,map[string]any{"title":"later"}); err != nil { t.Fatal(err) }
	peer := strings.Repeat("c",64)
	if err := f.ConfirmSnapshot(slug,peer,snap.Watermark); err != nil { t.Fatal(err) }
	after,events,err := f.ReadForPeer(slug,peer,10)
	if err != nil || after!=2 || len(events)!=1 || events[0].Sequence!=3 { t.Fatalf("snapshot catchup: %d %+v %v",after,events,err) }
	if err := s.DropTable(slug); err != nil { t.Fatal(err) }
	deleted,err := f.Snapshot(ctx,slug,10,MaxSnapshotBytes)
	if err != nil || deleted.Table!=nil || len(deleted.Rows)!=0 || deleted.Watermark<=3 { t.Fatalf("deletion: %+v %v",deleted,err) }
	if err := f.SetEnabled(slug,false); err != nil { t.Fatal(err) }
	if _,err := f.Snapshot(ctx,slug,10,MaxSnapshotBytes); !errors.Is(err,ErrChangeFeedDisabled) { t.Fatal(err) }
}
