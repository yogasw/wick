package team

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func folderNames(f Folders) string {
	var out []string
	for _, d := range f.Folders {
		out = append(out, d.Name)
	}
	return strings.Join(out, ",")
}

func TestFoldersValidate(t *testing.T) {
	ok := Folders{Folders: []Folder{{ID: "a", Name: "Ops"}, {ID: "b", Name: "Dev"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	cases := map[string]Folders{
		"dup name": {Folders: []Folder{{ID: "a", Name: "Ops"}, {ID: "b", Name: " ops "}}},
		"dup id":   {Folders: []Folder{{ID: "a", Name: "Ops"}, {ID: "a", Name: "Dev"}}},
		"no id":    {Folders: []Folder{{Name: "Ops"}}},
		"empty":    {Folders: []Folder{{ID: "a", Name: "  "}}},
		"too long": {Folders: []Folder{{ID: "a", Name: strings.Repeat("x", MaxFolderName+1)}}},
		"too many": {Folders: make([]Folder, MaxFolders+1)},
		"long id":  {Folders: []Folder{{ID: strings.Repeat("i", 65), Name: "Ops"}}},
	}
	for name, f := range cases {
		if err := f.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if err := cases["dup name"].Validate(); !errors.Is(err, ErrFolderTaken) {
		t.Errorf("dup name err = %v", err)
	}
	// A name of exactly the limit counts runes, not bytes.
	if err := (Folders{Folders: []Folder{{ID: "a", Name: strings.Repeat("é", MaxFolderName)}}}).Validate(); err != nil {
		t.Errorf("max runes: %v", err)
	}
}

func TestFoldersMove(t *testing.T) {
	var f Folders
	if err := f.Move("a1", "Ops"); err != nil {
		t.Fatal(err)
	}
	if err := f.Move("a2", "ops"); err != nil {
		t.Fatal(err)
	}
	if len(f.Folders) != 1 || f.NameOf("a1") != "Ops" || f.NameOf("a2") != "Ops" {
		t.Fatalf("move made %+v", f)
	}
	if err := f.Move("a1", ""); err != nil {
		t.Fatal(err)
	}
	if f.NameOf("a1") != "" || len(f.Folders) != 1 {
		t.Fatalf("move \"\" must make a1 loose and keep the folder: %+v", f)
	}
	if err := f.Move("a1", strings.Repeat("x", MaxFolderName+1)); !errors.Is(err, ErrFolderName) {
		t.Errorf("long name err = %v", err)
	}
}

func TestFoldersRenameDeleteOrder(t *testing.T) {
	var f Folders
	for _, n := range []string{"A", "B", "C"} {
		if _, err := f.Ensure(n); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.Move("x", "B")
	if err := f.Rename("b", "Bee"); err != nil || f.NameOf("x") != "Bee" {
		t.Fatalf("rename: %v %+v", err, f)
	}
	if err := f.Rename("Bee", "bee"); err != nil {
		t.Errorf("recasing its own name: %v", err)
	}
	if err := f.Rename("A", "C"); !errors.Is(err, ErrFolderTaken) {
		t.Errorf("rename onto C err = %v", err)
	}
	if err := f.Rename("nope", "Z"); !errors.Is(err, ErrFolderAbsent) {
		t.Errorf("rename missing err = %v", err)
	}
	if err := f.Order([]string{"C", "a"}); err != nil || folderNames(f) != "C,A,bee" {
		t.Fatalf("order: %v %s", err, folderNames(f))
	}
	if err := f.Order([]string{"nope"}); !errors.Is(err, ErrFolderAbsent) {
		t.Errorf("order missing err = %v", err)
	}
	if err := f.Delete("BEE"); err != nil {
		t.Fatal(err)
	}
	if folderNames(f) != "C,A" || f.NameOf("x") != "" || len(f.Place) != 0 {
		t.Fatalf("delete must drop the folder and its placements: %+v", f)
	}
	if err := f.Delete("BEE"); !errors.Is(err, ErrFolderAbsent) {
		t.Errorf("delete twice err = %v", err)
	}
}

func TestFoldersNormalDropsDanglingPlacements(t *testing.T) {
	f := DecodeFolders(`{"folders":[{"id":"f1","name":" Ops "}],"place":{"a1":"f1","a2":"gone","":"f1"}}`)
	if len(f.Place) != 1 || f.Place["a1"] != "f1" {
		t.Fatalf("place = %v", f.Place)
	}
	if f.Folders[0].Name != "Ops" {
		t.Errorf("name not trimmed: %q", f.Folders[0].Name)
	}
	if e := DecodeFolders("not json"); e.Folders == nil || e.Place == nil {
		t.Errorf("broken data must decode empty, not nil: %+v", e)
	}
}

func TestTeamFoldersSaveLoad(t *testing.T) {
	st := NewStore(testDB(t))
	ctx := context.Background()
	got, err := st.TeamFolders(ctx, "u1")
	if err != nil || len(got.Folders) != 0 || got.Place == nil {
		t.Fatalf("empty = %+v %v", got, err)
	}
	var f Folders
	_ = f.Move("a1", "Ops")
	_ = f.Move("a2", "Dev")
	if err := st.SaveTeamFolders(ctx, "u1", f); err != nil {
		t.Fatal(err)
	}
	_ = f.Delete("Dev")
	if err := st.SaveTeamFolders(ctx, "u1", f); err != nil {
		t.Fatalf("second save (upsert): %v", err)
	}
	got, err = st.TeamFolders(ctx, "u1")
	if err != nil || folderNames(got) != "Ops" || got.NameOf("a1") != "Ops" || got.NameOf("a2") != "" {
		t.Fatalf("round trip = %+v %v", got, err)
	}
	if other, _ := st.TeamFolders(ctx, "u2"); len(other.Folders) != 0 {
		t.Errorf("u2 reads u1's folders: %+v", other)
	}
	bad := Folders{Folders: []Folder{{ID: "a", Name: "X"}, {ID: "b", Name: "x"}}}
	if err := st.SaveTeamFolders(ctx, "u1", bad); err == nil {
		t.Error("invalid folders must not save")
	}
	if err := st.SaveTeamFolders(ctx, "", f); err == nil {
		t.Error("no user must not save")
	}
}
