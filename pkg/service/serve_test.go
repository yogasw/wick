package service

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

type echoSource struct{ done []string }

func (s *echoSource) Send(_ context.Context, t RemoteTurn) (RemoteSendResult, error) {
	return RemoteSendResult{Handle: "h1:" + t.Text, ContextID: "ctx-1"}, nil
}

func (s *echoSource) Receive(_ context.Context, h string) (<-chan RemoteEvent, error) {
	ch := make(chan RemoteEvent, 2)
	ch <- RemoteEvent{Kind: "text_delta", Text: strings.TrimPrefix(h, "h1:")}
	ch <- RemoteEvent{Kind: "done"}
	close(ch)
	return ch, nil
}

func (s *echoSource) Done(h string) { s.done = append(s.done, h) }

func TestManifestDeclaresRemoteSource(t *testing.T) {
	sm := Manifest(Module{Meta: Meta{Key: "echo"}, Routes: []Route{{Prefix: "/", Auth: Public}}, RemoteSource: &echoSource{}})
	if !sm.Has(wickplugin.CapRemoteSource) {
		t.Fatalf("capabilities = %v", sm.Capabilities)
	}
	if sm2 := Manifest(Module{Meta: Meta{Key: "x"}}); sm2.Has(wickplugin.CapRemoteSource) {
		t.Fatal("no RemoteSource must not declare remote_source")
	}
}

func TestMatchRouteLongestPrefix(t *testing.T) {
	sm := wickplugin.ServiceModule{Routes: []Route{{Prefix: "/", Auth: Public}, {Prefix: "/api", Auth: Token}, {Prefix: "/api/admin", Auth: Session}}}
	for path, want := range map[string]string{"/": Public, "/x": Public, "/api": Token, "/api/v1": Token, "/api/admin/k": Session, "/apix": Public} {
		r, ok := sm.MatchRoute(path)
		if !ok || r.Auth != want {
			t.Errorf("%s → %v %v, want %s", path, r, ok, want)
		}
	}
	if _, ok := (wickplugin.ServiceModule{Routes: []Route{{Prefix: "/api", Auth: Token}}}).MatchRoute("/other"); ok {
		t.Error("unmatched path must not match")
	}
}

func TestRemoteRPC(t *testing.T) {
	src := &echoSource{}
	srv := httptest.NewServer(Handler(Module{Meta: Meta{Key: "echo"}, RemoteSource: src}, nil))
	defer srv.Close()
	resp, err := http.Post(srv.URL+wickplugin.RemotePathSend, "application/json", strings.NewReader(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	var sr RemoteSendResult
	_ = json.NewDecoder(resp.Body).Decode(&sr)
	resp.Body.Close()
	if sr.Handle != "h1:hi" || sr.ContextID != "ctx-1" {
		t.Fatalf("send = %+v", sr)
	}
	resp, err = http.Get(srv.URL + wickplugin.RemotePathEvents + "?handle=" + sr.Handle)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if line, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var ev RemoteEvent
			_ = json.Unmarshal([]byte(line), &ev)
			kinds = append(kinds, ev.Kind+":"+ev.Text)
		}
	}
	resp.Body.Close()
	if strings.Join(kinds, ",") != "text_delta:hi,done:" {
		t.Fatalf("events = %v", kinds)
	}
	resp, _ = http.Post(srv.URL+wickplugin.RemotePathDone, "application/json", strings.NewReader(`{"handle":"h1:hi"}`))
	resp.Body.Close()
	if len(src.done) != 1 || src.done[0] != "h1:hi" {
		t.Fatalf("done = %v", src.done)
	}
}
