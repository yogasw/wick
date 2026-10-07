package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/pkg/memreport"
)

func rate(pid int, name string, rss uint64) memreport.ProcRate {
	return memreport.ProcRate{Proc: memreport.Proc{PID: pid, Name: name, RSSBytes: rss}}
}

func group(name string, rss uint64, members ...memreport.ProcRate) memreport.ProcGroup {
	return memreport.ProcGroup{Name: name, Count: len(members), RSSBytes: rss, Members: members}
}

// One executable name is routinely several people's spawns, so the group
// carries every owner — putting only the first one on the row would attribute
// somebody's memory to somebody else.
func TestGroupUsers_DistinctAndSorted(t *testing.T) {
	g := group("claude", 300, rate(10, "claude", 100), rate(11, "claude", 100), rate(12, "claude", 100))
	owners := map[int]string{10: "yoga", 11: "anggun", 12: "yoga"}
	got := groupUsers(g, owners)
	if len(got) != 2 || got[0] != "anggun" || got[1] != "yoga" {
		t.Fatalf("got %v, want [anggun yoga]", got)
	}
}

func TestGroupUsers_NoneWhenNothingAttributed(t *testing.T) {
	g := group("YDService", 100, rate(99, "YDService", 100))
	if got := groupUsers(g, map[int]string{10: "yoga"}); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// Sorting by user answers "who is running what", so the processes nobody here
// started go last — they are the majority of the list and the least relevant.
func TestSortGroupsByUser_NamedFirstUnattributedLast(t *testing.T) {
	groups := []memreport.ProcGroup{
		group("YDService", 500, rate(99, "YDService", 500)),
		group("claude", 100, rate(10, "claude", 100)),
		group("codex", 200, rate(20, "codex", 200)),
	}
	owners := map[int]string{10: "yoga", 20: "anggun"}
	sortGroupsByUser(groups, owners)
	want := []string{"codex", "claude", "YDService"} // anggun, yoga, then unowned
	for i, w := range want {
		if groups[i].Name != w {
			t.Fatalf("position %d = %s, want %s (all: %v)", i, groups[i].Name, w,
				[]string{groups[0].Name, groups[1].Name, groups[2].Name})
		}
	}
}

// Ties inside one person's rows fall back to memory, so the heaviest thing
// somebody is running is the first one they see.
func TestSortGroupsByUser_TiesOnMemory(t *testing.T) {
	groups := []memreport.ProcGroup{
		group("small", 100, rate(10, "small", 100)),
		group("big", 900, rate(11, "big", 900)),
	}
	sortGroupsByUser(groups, map[int]string{10: "yoga", 11: "yoga"})
	if groups[0].Name != "big" {
		t.Fatalf("got %s first, want big", groups[0].Name)
	}
}

// Typing a name is the fastest route to "what is MINE doing" — the reason the
// column exists.
func TestGroupMatches_FindsByOwnerName(t *testing.T) {
	g := group("claude", 100, rate(10, "claude", 100))
	owners := map[int]string{10: "Yoga Setiawan"}
	if !groupMatches(g, "yoga", owners) {
		t.Fatal("search by owner name did not match")
	}
	if groupMatches(g, "anggun", owners) {
		t.Fatal("search matched a different owner")
	}
}

func TestGroupMatches_StillMatchesNameAndCmdline(t *testing.T) {
	m := rate(10, "node", 100)
	m.Cmdline = "node /srv/thing.js"
	g := group("node", 100, m)
	if !groupMatches(g, "node", nil) {
		t.Fatal("name match broke")
	}
	if !groupMatches(g, "thing.js", nil) {
		t.Fatal("cmdline match broke")
	}
}
