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

// newRailUser stores a user whose rail labels choice is labels; nil is a
// user who never chose.
func newRailUser(t *testing.T, labels *bool) (*Service, string) {
	t.Helper()
	db := newLoginSQLite(t)
	u := &entity.User{ID: "u1", Email: "u1@example.com", Name: "U1", Approved: true, Role: entity.RoleUser}
	u.Metadata.Rail = entity.RailPrefs{Order: []string{"files", "todos"}, Labels: labels}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &Service{repo: newRepo(db)}, u.ID
}

func boolp(b bool) *bool { return &b }

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
	s, id := newRailUser(t, boolp(false))
	next := entity.RailPrefs{Order: []string{"todos", "files"}, Hidden: []string{"files"}}
	if err := s.SetRailPrefs(context.Background(), id, next); err != nil {
		t.Fatalf("SetRailPrefs: %v", err)
	}
	got := railOf(t, s, id)
	if got.LabelsOn() || got.Labels == nil {
		t.Fatalf("Labels = %v after a layout save, want the explicit false kept", got.Labels)
	}
	if !reflect.DeepEqual(got.Order, next.Order) || !reflect.DeepEqual(got.Hidden, next.Hidden) {
		t.Fatalf("layout = %v / %v, want %v / %v", got.Order, got.Hidden, next.Order, next.Hidden)
	}

	// A client that does send labels cannot flip it through the rail either.
	s2, id2 := newRailUser(t, boolp(false))
	if err := s2.SetRailPrefs(context.Background(), id2, entity.RailPrefs{Order: []string{"files"}, Labels: boolp(true)}); err != nil {
		t.Fatalf("SetRailPrefs: %v", err)
	}
	if railOf(t, s2, id2).LabelsOn() {
		t.Fatalf("labels on after a layout save, want it kept off")
	}

	// A user who never chose stays on the default (labels on).
	s3, id3 := newRailUser(t, nil)
	if err := s3.SetRailPrefs(context.Background(), id3, entity.RailPrefs{Order: []string{"files"}, Labels: boolp(false)}); err != nil {
		t.Fatalf("SetRailPrefs: %v", err)
	}
	if got := railOf(t, s3, id3); got.Labels != nil || !got.LabelsOn() {
		t.Fatalf("Labels = %v after a layout save, want it left unset", got.Labels)
	}
}

func TestUpdatePreferencesRailLabels(t *testing.T) {
	cases := []struct {
		name  string
		start *bool
		value *string
		want  *bool
	}{
		{"labels turns it on", boolp(false), railValue(entity.RailStyleLabels), boolp(true)},
		{"icons turns it off", boolp(true), railValue(entity.RailStyleIcons), boolp(false)},
		{"icons is stored for a user who never chose", nil, railValue(entity.RailStyleIcons), boolp(false)},
		{"labels is stored explicitly for a user who never chose", nil, railValue(entity.RailStyleLabels), boolp(true)},
		{"missing field leaves it on", boolp(true), nil, boolp(true)},
		{"missing field leaves it off", boolp(false), nil, boolp(false)},
		{"missing field leaves it unset", nil, nil, nil},
		{"unknown value is ignored", boolp(false), railValue("foo"), boolp(false)},
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
			if got := railOf(t, s, id); !reflect.DeepEqual(got.Labels, c.want) {
				t.Fatalf("Labels = %v, want %v", fmtBoolp(got.Labels), fmtBoolp(c.want))
			}
			if got := railOf(t, s, id); !reflect.DeepEqual(got.Order, []string{"files", "todos"}) {
				t.Fatalf("Order = %v, want the layout untouched", got.Order)
			}
		})
	}
}

func railValue(s string) *string { return &s }

func fmtBoolp(b *bool) string {
	if b == nil {
		return "unset"
	}
	if *b {
		return "true"
	}
	return "false"
}
