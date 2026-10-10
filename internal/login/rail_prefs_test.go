package login

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func newRailUser(t *testing.T, labels bool) (*Service, string) {
	t.Helper()
	db := newLoginSQLite(t)
	u := &entity.User{ID: "u1", Email: "u1@example.com", Name: "U1", Approved: true, Role: entity.RoleUser}
	u.Metadata.Rail = entity.RailPrefs{Order: []string{"files", "todos"}, Labels: labels}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &Service{repo: newRepo(db)}, u.ID
}

func railOf(t *testing.T, s *Service, id string) entity.RailPrefs {
	t.Helper()
	u, err := s.repo.GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	return u.Metadata.Rail
}

// The rail saves its arrangement without the labels flag (the strip never
// sends it), so a drag on the strip must not switch a profile choice off.
func TestSetRailPrefsKeepsLabels(t *testing.T) {
	s, id := newRailUser(t, true)
	next := entity.RailPrefs{Order: []string{"todos", "files"}, Hidden: []string{"files"}}
	if err := s.SetRailPrefs(context.Background(), id, next); err != nil {
		t.Fatalf("SetRailPrefs: %v", err)
	}
	got := railOf(t, s, id)
	if !got.Labels {
		t.Fatalf("Labels = false after a layout save, want it kept true")
	}
	if !reflect.DeepEqual(got.Order, next.Order) || !reflect.DeepEqual(got.Hidden, next.Hidden) {
		t.Fatalf("layout = %v / %v, want %v / %v", got.Order, got.Hidden, next.Order, next.Hidden)
	}

	// A client that does send labels cannot flip it through the rail either.
	s2, id2 := newRailUser(t, false)
	if err := s2.SetRailPrefs(context.Background(), id2, entity.RailPrefs{Order: []string{"files"}, Labels: true}); err != nil {
		t.Fatalf("SetRailPrefs: %v", err)
	}
	if railOf(t, s2, id2).Labels {
		t.Fatalf("Labels = true after a layout save, want it kept false")
	}
}

func TestUpdatePreferencesRailLabels(t *testing.T) {
	cases := []struct {
		name  string
		start bool
		value *string
		want  bool
	}{
		{"labels turns it on", false, railValue(entity.RailStyleLabels), true},
		{"icons turns it off", true, railValue(entity.RailStyleIcons), false},
		{"missing field leaves it on", true, nil, true},
		{"missing field leaves it off", false, nil, false},
		{"unknown value is ignored", true, railValue("foo"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, id := newRailUser(t, c.start)
			form := url.Values{"home_view": {entity.HomeViewCompact}}
			if c.value != nil {
				form.Set("rail_labels", *c.value)
			}
			r := httptest.NewRequest(http.MethodPost, "/profile/preferences", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r = r.WithContext(WithUser(r.Context(), &entity.User{ID: id}, nil))
			w := httptest.NewRecorder()
			(&Handler{svc: s}).updatePreferences(w, r)
			if w.Code != http.StatusFound {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
			}
			if got := railOf(t, s, id); got.Labels != c.want {
				t.Fatalf("Labels = %v, want %v", got.Labels, c.want)
			}
			if got := railOf(t, s, id); !reflect.DeepEqual(got.Order, []string{"files", "todos"}) {
				t.Fatalf("Order = %v, want the layout untouched", got.Order)
			}
		})
	}
}

func railValue(s string) *string { return &s }
