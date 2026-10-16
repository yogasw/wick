package view

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestGroupRowsSkipsInternal(t *testing.T) {
	rows := []entity.Config{
		{Key: "token", Group: "Connection"},
		{Key: "expires", Group: "Connection", Internal: true},
		{Key: "only_internal", Group: "Bookkeeping", Internal: true},
	}
	groups := groupRows(rows)
	if len(groups) != 1 || groups[0].Title != "Connection" {
		t.Fatalf("groups = %+v, want only Connection", groups)
	}
	if len(groups[0].Simple) != 1 || groups[0].Simple[0].Key != "token" {
		t.Errorf("Connection fields = %+v, want only token", groups[0].Simple)
	}
}
