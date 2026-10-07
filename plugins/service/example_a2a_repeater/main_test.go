package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/service"
)

func rpc(t *testing.T, url, method, contextID, text string) *http.Response {
	t.Helper()
	msg := map[string]any{"role": "ROLE_USER", "messageId": "m-" + text, "parts": []any{map[string]any{"text": text}}}
	if contextID != "" {
		msg["contextId"] = contextID
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{"message": msg}})
	resp, err := http.Post(url+"/", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestManifest(t *testing.T) {
	sm := service.Manifest(module())
	if sm.Meta.Key != "example_a2a_repeater" || !sm.Has(wickplugin.CapRemoteSource) || sm.Routes[0].Auth != service.Public {
		t.Fatalf("manifest = %+v", sm)
	}
}

func TestA2AWire(t *testing.T) {
	srv := httptest.NewServer(service.Handler(module(), nil))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/agent.json")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("card: %v %v", err, resp.StatusCode)
	}
	var card map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&card)
	resp.Body.Close()
	if card["name"] != "Echo bot" {
		t.Fatalf("card = %v", card)
	}

	resp = rpc(t, srv.URL, "SendMessage", "ctx-a", "hello")
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	b, _ := json.Marshal(out)
	if got := partsText(b); got != "echo: hello (turn 1 of conv-1)" {
		t.Fatalf("send = %s", b)
	}
	// Same contextId → same bot conversation, next turn.
	resp = rpc(t, srv.URL, "SendMessage", "ctx-a", "again")
	b, _ = readAll(resp)
	if got := partsText(b); got != "echo: again (turn 2 of conv-1)" {
		t.Fatalf("second send = %s", b)
	}

	resp = rpc(t, srv.URL, "SendStreamingMessage", "ctx-b", "stream me")
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("stream content-type = %q", ct)
	}
	n := 0
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data:") {
			n++
		}
	}
	resp.Body.Close()
	if n < 4 {
		t.Fatalf("stream events = %d", n)
	}
}

// partsText joins the artifact text parts of a JSON-RPC task result.
func partsText(b []byte) string {
	var out struct {
		Result struct {
			Task struct {
				Artifacts []struct {
					Parts []struct{ Text string } `json:"parts"`
				} `json:"artifacts"`
			} `json:"task"`
		} `json:"result"`
	}
	_ = json.Unmarshal(b, &out)
	s := ""
	for _, a := range out.Result.Task.Artifacts {
		for _, p := range a.Parts {
			s += p.Text
		}
	}
	return s
}

func readAll(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	var out map[string]any
	err := json.NewDecoder(resp.Body).Decode(&out)
	b, _ := json.Marshal(out)
	return b, err
}

func TestRemoteSource(t *testing.T) {
	srv := httptest.NewServer(service.Handler(module(), nil))
	defer srv.Close()
	resp, _ := http.Post(srv.URL+wickplugin.RemotePathSend, "application/json", strings.NewReader(`{"text":"hi"}`))
	var sr service.RemoteSendResult
	_ = json.NewDecoder(resp.Body).Decode(&sr)
	resp.Body.Close()
	resp, _ = http.Get(srv.URL + wickplugin.RemotePathEvents + "?handle=" + sr.Handle)
	var text string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var ev service.RemoteEvent
			_ = json.Unmarshal([]byte(d), &ev)
			text += ev.Text
		}
	}
	resp.Body.Close()
	if text != "echo: hi (turn 1 of conv-1)" || sr.ContextID == "" {
		t.Fatalf("text=%q ctx=%q", text, sr.ContextID)
	}
}
