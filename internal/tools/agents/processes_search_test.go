package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/pkg/memreport"
)

func groupWith(name string, cmds ...string) memreport.ProcGroup {
	g := memreport.ProcGroup{Name: name, Count: len(cmds)}
	for i, c := range cmds {
		g.Members = append(g.Members, memreport.ProcRate{
			Proc: memreport.Proc{PID: 100 + i, Name: name, Cmdline: c},
		})
	}
	return g
}

// Searching the command line is the point of having it. The rows an
// operator most needs to find are the ambiguous ones — several "node"
// processes where only the arguments say which is which — and matching
// on name alone returns all of them or none.
func TestGroupMatches_FindsByCommand(t *testing.T) {
	g := groupWith("node",
		"node /srv/api/server.js --port 8080",
		"node /srv/worker/queue.js")

	if !groupMatches(g, "queue.js") {
		t.Fatal("a term that appears only in a member's command did not match")
	}
	if !groupMatches(g, "8080") {
		t.Fatal("an argument value did not match")
	}
}

// Name matching must keep working — it is the common case.
func TestGroupMatches_StillFindsByName(t *testing.T) {
	if !groupMatches(groupWith("chrome.exe", ""), "chrome") {
		t.Fatal("a name substring did not match")
	}
}

// A term in neither place must not match, or search returns everything.
func TestGroupMatches_RejectsNonMatches(t *testing.T) {
	g := groupWith("node", "node server.js")
	if groupMatches(g, "postgres") {
		t.Fatal("an unrelated term matched")
	}
}

// Callers lower-case the needle once rather than per member; the haystack
// side must lower-case too or a capitalised path never matches.
func TestGroupMatches_IsCaseInsensitive(t *testing.T) {
	g := groupWith("Code.exe", `C:\Program Files\Microsoft VS Code\Code.exe`)

	if !groupMatches(g, "program files") {
		t.Fatal("a lower-cased term did not match a capitalised command")
	}
	if !groupMatches(g, "code.exe") {
		t.Fatal("a lower-cased term did not match a capitalised name")
	}
}

// Kernel threads and other users' processes report no command; that must
// be skipped rather than treated as an empty string that matches
// everything.
func TestGroupMatches_EmptyCommandIsNotAWildcard(t *testing.T) {
	g := groupWith("kthreadd", "")
	if groupMatches(g, "anything") {
		t.Fatal("a group with no command matched an unrelated term")
	}
}
