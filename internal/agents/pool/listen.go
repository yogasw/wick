package pool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog/log"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

// listen.go backs Slack listen mode (agentchannels.ListenStore): the
// per-thread state on the session meta, the LISTEN.md documents, and the
// channels a bot listens to as a whole.

// ListenDocName is the file that shapes how an agent listens: one wick-wide
// default at the agents root, one per agent in its project folder.
const ListenDocName = "LISTEN.md"

// listenDocMax bounds what is read from a LISTEN.md; the rest is ignored.
const listenDocMax = 16 * 1024

// ListenThread reports what a "listen" request stored on sessionID.
func (p *Pool) ListenThread(sessionID string) agentchannels.ListenThread {
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil {
		return agentchannels.ListenThread{}
	}
	return agentchannels.ListenThread{
		Prompt:   sess.Meta.ListenPrompt,
		DelaySec: sess.Meta.ListenDelaySec,
		Stopped:  sess.Meta.ListenStopped,
	}
}

// SetListenThread stores lt on sessionID. A missing session is logged and
// skipped, like SetAutoReply.
func (p *Pool) SetListenThread(sessionID string, lt agentchannels.ListenThread) {
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil {
		log.Warn().Str("session", sessionID).Err(err).Msg("pool: set listen — session load failed")
		return
	}
	sess.Meta.ListenPrompt = lt.Prompt
	sess.Meta.ListenDelaySec = lt.DelaySec
	sess.Meta.ListenStopped = lt.Stopped
	if err := session.SaveMeta(p.cfg.Layout, sessionID, sess.Meta); err != nil {
		log.Warn().Str("session", sessionID).Err(err).Msg("pool: set listen — save failed")
	}
}

// ListenDocs returns the wick-wide default LISTEN.md (agents root) and the
// one in the session's project folder. Read on every call: an edit applies
// to the next message without a restart.
func (p *Pool) ListenDocs(sessionID string) (wickDefault, agent string) {
	wickDefault = readListenDoc(filepath.Join(p.cfg.Layout.BaseDir, ListenDocName))
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil || sess.Meta.ProjectID == "" {
		return wickDefault, ""
	}
	dir, err := project.ResolvePath(p.cfg.Layout, sess.Meta.ProjectID)
	if err != nil || dir == "" {
		return wickDefault, ""
	}
	return wickDefault, readListenDoc(filepath.Join(dir, ListenDocName))
}

func readListenDoc(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, listenDocMax)
	n, _ := f.Read(buf)
	return string(buf[:n])
}

// listenChannelsMu guards listen-channels.json.
var listenChannelsMu sync.Mutex

func (p *Pool) listenChannelsFile() string {
	return filepath.Join(p.cfg.Layout.BaseDir, "listen-channels.json")
}

// loadListenChannels reads instance → chat ids. Caller holds the mutex.
func (p *Pool) loadListenChannels() map[string][]string {
	out := map[string][]string{}
	b, err := os.ReadFile(p.listenChannelsFile())
	if err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// ListenChannel reports whether instance listens to every thread it has in
// chatID (scope "channel").
func (p *Pool) ListenChannel(instance, chatID string) bool {
	listenChannelsMu.Lock()
	defer listenChannelsMu.Unlock()
	for _, id := range p.loadListenChannels()[instance] {
		if id == chatID {
			return true
		}
	}
	return false
}

// SetListenChannel adds or removes chatID from instance's listened channels.
func (p *Pool) SetListenChannel(instance, chatID string, on bool) {
	listenChannelsMu.Lock()
	defer listenChannelsMu.Unlock()
	all := p.loadListenChannels()
	var kept []string
	found := false
	for _, id := range all[instance] {
		if id == chatID {
			found = true
			if !on {
				continue
			}
		}
		kept = append(kept, id)
	}
	if on && !found {
		kept = append(kept, chatID)
	}
	if len(kept) == 0 {
		delete(all, instance)
	} else {
		all[instance] = kept
	}
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return
	}
	tmp := p.listenChannelsFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Warn().Err(err).Msg("pool: save listen channels failed")
		return
	}
	_ = os.Rename(tmp, p.listenChannelsFile())
}

var _ agentchannels.ListenStore = (*Pool)(nil)
