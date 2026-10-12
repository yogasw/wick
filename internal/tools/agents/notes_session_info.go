package agents

import (
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/pkg/tool"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
)

// sessionPerson is one human who wrote in a session, as the rail's Session
// tab lists them: who, how many messages, and whether they own the session.
type sessionPerson struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Handle   string `json:"handle,omitempty"`
	Channel  string `json:"channel,omitempty"`
	Messages int    `json:"messages"`
	Owner    bool   `json:"owner,omitempty"`
}

// sessionPeople counts the user turns of a conversation per writer. A turn
// is the writer's wick account when the sender maps to one, else the
// platform identity; a turn with no sender (typed in the web UI before
// senders were recorded) is the owner's. System turns are not people.
// Most messages first, then by first appearance.
func sessionPeople(turns []store.ConversationTurn, owner string) []sessionPerson {
	idx := map[string]int{}
	var out []sessionPerson
	for _, t := range turns {
		if t.Role != "user" {
			continue
		}
		key, p := owner, sessionPerson{ID: owner}
		if s := t.Sender; s != nil && (s.WickUserID != "" || s.ID != "") {
			key = s.WickUserID
			if key == "" {
				key = s.Channel + ":" + s.ID
			}
			p = sessionPerson{ID: key, Name: s.Name, Handle: s.Handle, Channel: s.Channel}
		}
		if key == "" {
			continue
		}
		i, ok := idx[key]
		if !ok {
			p.Owner = owner != "" && key == owner
			idx[key] = len(out)
			out = append(out, p)
			i = len(out) - 1
		}
		out[i].Messages++
		if out[i].Name == "" && p.Name != "" {
			out[i].Name = p.Name
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Messages > out[b].Messages })
	return out
}

// sessionInfo is the Session tab's Info block: who started it, where it
// lives and when. Names are filled by the caller (one batched lookup).
func sessionInfo(sess session.Session, projectName string) map[string]any {
	info := map[string]any{
		"started_by":   sess.Meta.UserID,
		"project_id":   sess.Meta.ProjectID,
		"project_name": projectName,
		"origin":       sess.Meta.Origin,
	}
	if !sess.Meta.CreatedAt.IsZero() {
		info["created_at"] = sess.Meta.CreatedAt.UTC().Format(time.RFC3339)
	}
	return info
}

// sessionAgent is one Team agent that answered in a session, counted over
// the WHOLE conversation — the rail only has the pages it loaded, so a
// count taken there misses an agent that spoke early in a long chat. Keyed
// like the rail's own list: the agent plus whom it works for.
type sessionAgent struct {
	AgentID     string `json:"agent_id"`
	Handle      string `json:"handle,omitempty"`
	Name        string `json:"name,omitempty"`
	OwnerID     string `json:"owner_id"`
	OwnerHandle string `json:"owner_handle,omitempty"`
	Via         string `json:"via,omitempty"`
	Replies     int    `json:"replies"`
}

// sessionAgents counts the assistant turns another person's agent said,
// in order of first reply. Only turns with an owner count: those are the
// ones a ticket session records for somebody else's agent.
func sessionAgents(turns []store.ConversationTurn) []sessionAgent {
	idx := map[string]int{}
	out := []sessionAgent{}
	for _, t := range turns {
		sp := t.Speaker
		if t.Role != "assistant" || sp == nil || sp.OwnerID == "" {
			continue
		}
		key := sp.AgentID + "\x00" + sp.OwnerID
		if i, ok := idx[key]; ok {
			out[i].Replies++
			continue
		}
		idx[key] = len(out)
		out = append(out, sessionAgent{
			AgentID: sp.AgentID, Handle: sp.Handle, Name: sp.Name,
			OwnerID: sp.OwnerID, OwnerHandle: sp.OwnerHandle, Via: sp.Via, Replies: 1,
		})
	}
	return out
}

// sessionTally is what one read of a conversation file yields for the
// Session tab, and the file state it was read at.
type sessionTally struct {
	mod    time.Time
	size   int64
	people []sessionPerson
	agents []sessionAgent
}

// sessionTallies caches the tally per session. The rail re-reads notes on
// every reload; without this each one parsed the whole conversation, tool
// output and all. A write changes the file's size or mtime, which is the
// whole invalidation.
var sessionTallies sync.Map // session id → sessionTally

// tallySession is the session's people and agents, from the cache while
// the conversation file is unchanged. A file that cannot be read yields
// nothing — the tab shows no people rather than failing the reply.
func tallySession(sess session.Session) ([]sessionPerson, []sessionAgent) {
	fi, err := os.Stat(globalLayout.SessionConversation(sess.ID))
	if err != nil {
		return []sessionPerson{}, []sessionAgent{}
	}
	if v, ok := sessionTallies.Load(sess.ID); ok {
		if t := v.(sessionTally); t.mod.Equal(fi.ModTime()) && t.size == fi.Size() {
			// A copy: the caller fills names into the people it gets.
			return append([]sessionPerson(nil), t.people...), t.agents
		}
	}
	turns, err := loadConversation(globalLayout, sess.ID)
	if err != nil {
		return []sessionPerson{}, []sessionAgent{}
	}
	t := sessionTally{mod: fi.ModTime(), size: fi.Size(), people: sessionPeople(turns, sess.Meta.UserID), agents: sessionAgents(turns)}
	if t.people == nil {
		t.people = []sessionPerson{}
	}
	sessionTallies.Store(sess.ID, t)
	return append([]sessionPerson(nil), t.people...), t.agents
}

// addSessionInfo puts the Session tab's data on a notes reply for the
// session the scope was checked through: its Info block (session_info),
// the people who wrote (session_people) and the agents that answered
// (session_agents). Only for a viewer who may open the session itself
// (ownsSession): project access alone reaches a member's backing session
// in a group or ticket room, which is that person's alone, and must not
// hand out who wrote in it.
func addSessionInfo(c *tool.Ctx, out map[string]any, sess session.Session) {
	if !ownsSession(c, sess) {
		return
	}
	name := ""
	if p, ok := globalMgr.Registry().Project(sess.Meta.ProjectID); ok {
		name = p.Meta.Name
	}
	info := sessionInfo(sess, name)
	people, agents := tallySession(sess)
	ids := map[string]bool{sess.Meta.UserID: sess.Meta.UserID != ""}
	for _, p := range people {
		if !strings.Contains(p.ID, ":") {
			ids[p.ID] = true
		}
	}
	names := userNames(c, ids)
	if n := names[sess.Meta.UserID]; n != "" {
		info["started_by_name"] = n
	}
	for i := range people {
		if n := names[people[i].ID]; n != "" {
			people[i].Name = n
		}
	}
	out["session_info"] = info
	out["session_people"] = people
	out["session_agents"] = agents
}
