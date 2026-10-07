package pluginremote_test

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/remote/pluginremote"
	"github.com/yogasw/wick/internal/services/plugin/plugintest"
)

// One full turn through a real host: Team remote runner → pluginremote →
// example_a2a_repeater process (Send → text_delta events → done → Done).
func TestFullTurnThroughRealPlugin(t *testing.T) {
	h := plugintest.StartRepeater(t)
	src := pluginremote.NewSource(plugintest.RepeaterKey, plugintest.Transport(h))
	dir := t.TempDir()
	for turn, want := range []string{"echo: hi (turn 1 of conv-1)", "echo: again (turn 2 of conv-1)"} {
		msg := []string{"hi", "again"}[turn]
		p, err := remote.Spawner{Source: src}.Spawn(context.Background(), provider.SpawnOptions{InitialMessage: msg, SessionDir: dir, SessionID: "s1"})
		if err != nil {
			t.Fatal(err)
		}
		var text, result string
		var isErr bool
		sc := bufio.NewScanner(p.Stdout())
		for sc.Scan() {
			var l struct {
				Type    string `json:"type"`
				Result  string `json:"result"`
				IsError bool   `json:"is_error"`
				Message struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
				t.Fatal(err)
			}
			if l.Type == "assistant" {
				for _, c := range l.Message.Content {
					text += c.Text
				}
			}
			if l.Type == "result" {
				result, isErr = l.Result, l.IsError
				break
			}
		}
		_ = p.Kill()
		_ = p.Wait()
		if isErr || strings.TrimSpace(result) != want || strings.TrimSpace(text) != want {
			t.Fatalf("turn %d: text=%q result=%q isErr=%v, want %q", turn+1, text, result, isErr, want)
		}
	}
	// Both turns ran in one bot conversation ("turn 2 of conv-1" above),
	// resumed from the context id kept in the session dir.
	if got := src.ResumeID(dir); !strings.HasPrefix(got, "plugin:"+plugintest.RepeaterKey+":") {
		t.Fatalf("resume id = %q", got)
	}
}
