package savedresets

import (
	"errors"
	"testing"
	"time"
)

func TestRegistry(t *testing.T) {
	const typ = "fake-plugin"
	if _, err := Read(typ, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unregistered: err = %v, want ErrUnsupported", err)
	}
	Register(typ, func([]string) (*SavedResets, error) { return &SavedResets{Supported: true, Available: 3}, nil })
	t.Cleanup(func() { Register(typ, nil) })
	r, err := Read(typ, nil)
	if err != nil || r.Available != 3 {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	Register(typ, nil)
	if _, ok := ReaderFor(typ); ok {
		t.Fatal("nil reader must unregister")
	}
}

func TestSortAndSoonest(t *testing.T) {
	a := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	b := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	items := []Reset{{ID: "undated"}, {ID: "a", ExpiresAt: a}, {ID: "b", ExpiresAt: b}}
	SortItems(items)
	if items[0].ID != "b" || items[1].ID != "a" || items[2].ID != "undated" {
		t.Fatalf("order = %+v", items)
	}
	if got := SoonestExpiry(&SavedResets{Items: items}); !got.Equal(b) {
		t.Fatalf("soonest = %v", got)
	}
	if !SoonestExpiry(nil).IsZero() {
		t.Fatal("nil must give zero")
	}
}

func TestParseTime(t *testing.T) {
	want := time.Date(2026, 10, 11, 14, 0, 0, 0, time.UTC)
	for _, v := range []any{"2026-10-11T14:00:00Z", float64(want.Unix()), float64(want.UnixMilli())} {
		if got := ParseTime(v); !got.Equal(want) {
			t.Errorf("ParseTime(%v) = %v", v, got)
		}
	}
	if !ParseTime("junk").IsZero() || !ParseTime(nil).IsZero() {
		t.Error("junk must give zero")
	}
}
