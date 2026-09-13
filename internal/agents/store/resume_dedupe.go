package store

import (
	"encoding/json"
	"os"
	"strings"
)

// resume_dedupe.go removes the paragraph a reader has already seen.
//
// When a turn is cut off — a drain that ran out of patience, a process
// replaced, an operator pressing stop — wick writes what was delivered so far
// as an assistant turn marked Interrupted. The agent is then resumed, and the
// provider replays the message FROM THE START: same opening, then the part
// that never made it. Both get written, so the conversation shows the same
// paragraph twice, the second time with a few more sentences on the end. It
// reads like the agent repeating itself, which is exactly what it looks like
// to the person waiting.
//
// So a resumed turn drops the prefix its interrupted predecessor already
// published. Only an EXACT prefix is trimmed: an agent that resumes by
// rewording starts a genuinely different message, and silently cutting the
// front off that would corrupt the record rather than tidy it.

// tailBytes bounds how much of the conversation is read to find the previous
// turn. A turn is a few KB at most; reading the whole file on every flush
// would make a long session pay for its own length.
const tailBytes = 128 * 1024

// dedupeResumedPrefix returns body with the interrupted predecessor's text
// removed, when body is a replay of it.
func (s *Store) dedupeResumedPrefix(body string) string {
	if body == "" {
		return body
	}
	prev, ok := s.lastInterruptedText()
	if !ok || prev == "" || len(prev) >= len(body) {
		// len(prev) >= len(body): not a continuation. Equal text means the
		// replay added nothing, and the turn is written as-is so the record
		// still shows the agent produced it.
		return body
	}
	if !strings.HasPrefix(body, prev) {
		return body
	}
	return strings.TrimLeft(strings.TrimPrefix(body, prev), " \n")
}

// lastInterruptedText reads the last assistant turn of this session and
// returns its text when it was interrupted.
//
// Read from disk rather than remembered in memory: the interruption and the
// resume are often in DIFFERENT processes — that is what a handover is — and
// a field on this Store would be empty in exactly the case that matters.
func (s *Store) lastInterruptedText() (string, bool) {
	path := s.layout.SessionConversation(s.sessionID)
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", false
	}
	start := int64(0)
	if st.Size() > tailBytes {
		start = st.Size() - tailBytes
	}
	buf := make([]byte, st.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && len(buf) == 0 {
		return "", false
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.HasPrefix(line, "{") {
			continue // a partial first line from the byte window, or the header
		}
		var t ConversationTurn
		if json.Unmarshal([]byte(line), &t) != nil {
			continue
		}
		if t.Role == "" {
			continue // not a turn line (file header)
		}
		if t.Role != "assistant" {
			return "", false // the last word was not the agent's: nothing was cut off
		}
		return t.Text, t.Interrupted
	}
	return "", false
}
