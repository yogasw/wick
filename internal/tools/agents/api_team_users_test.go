package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestTeamProjectUsersOthers(t *testing.T) {
	u := teamProjectUsers{
		agents:   map[string]int{"p1": 3, "p2": 1},
		sessions: map[string]int{"p1": 2, "p3": 4},
	}
	cases := []struct {
		project string
		want    int
	}{
		{"p1", 4}, // two other agents + two web/channel sessions
		{"p2", 0}, // alone on its project
		{"p3", 4}, // agent row missing from the count still sees sessions
		{"", 0},
	}
	for _, c := range cases {
		if got := u.others(entity.AgentPersona{ProjectID: c.project}); got != c.want {
			t.Errorf("others(%q) = %d, want %d", c.project, got, c.want)
		}
	}
	if got := (teamProjectUsers{}).others(entity.AgentPersona{ProjectID: "p1"}); got != 0 {
		t.Errorf("zero value = %d", got)
	}
}
