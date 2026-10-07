package service

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// injectSource is echoSource that also takes a message mid-turn.
type injectSource struct {
	echoSource
	got []string
}

func (s *injectSource) Inject(_ context.Context, handle, text string) error {
	s.got = append(s.got, handle+"|"+text)
	return nil
}

func TestRemoteInjectRoute(t *testing.T) {
	describe := func(url string) map[string]any {
		resp, err := http.Get(url + wickplugin.RemotePathDescribe)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	post := func(url string) int {
		resp, err := http.Post(url+wickplugin.RemotePathInject, "application/json", strings.NewReader(`{"handle":"h1","text":"more"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	plain := httptest.NewServer(Handler(Module{Meta: Meta{Key: "echo"}, RemoteSource: &echoSource{}}, nil))
	defer plain.Close()
	if code := post(plain.URL); code != http.StatusNotImplemented {
		t.Fatalf("inject without RemoteInjector = %d, want 501", code)
	}
	if _, ok := describe(plain.URL)["inject"]; ok {
		t.Fatal("describe announces inject for a source without it")
	}
	if m := Manifest(Module{RemoteSource: &echoSource{}}); m.Has(wickplugin.CapRemoteInject) {
		t.Fatal("manifest declares remote_inject without RemoteInjector")
	}

	src := &injectSource{}
	srv := httptest.NewServer(Handler(Module{Meta: Meta{Key: "echo"}, RemoteSource: src}, nil))
	defer srv.Close()
	if code := post(srv.URL); code != http.StatusNoContent {
		t.Fatalf("inject = %d, want 204", code)
	}
	if len(src.got) != 1 || src.got[0] != "h1|more" {
		t.Fatalf("injected = %v", src.got)
	}
	if describe(srv.URL)["inject"] != true {
		t.Fatal("describe does not announce inject")
	}
	if m := Manifest(Module{RemoteSource: src}); !m.Has(wickplugin.CapRemoteInject) {
		t.Fatal("manifest misses remote_inject")
	}
}

// cancelSource is echoSource that can also stop a turn on the remote.
type cancelSource struct {
	echoSource
	got []string
}

func (s *cancelSource) Cancel(_ context.Context, handle string) error {
	s.got = append(s.got, handle)
	return nil
}

func TestRemoteCancelRoute(t *testing.T) {
	describe := func(url string) map[string]any {
		resp, err := http.Get(url + wickplugin.RemotePathDescribe)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	post := func(url string) int {
		resp, err := http.Post(url+wickplugin.RemotePathCancel, "application/json", strings.NewReader(`{"handle":"h1"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	plain := httptest.NewServer(Handler(Module{Meta: Meta{Key: "echo"}, RemoteSource: &echoSource{}}, nil))
	defer plain.Close()
	if code := post(plain.URL); code != http.StatusNotImplemented {
		t.Fatalf("cancel without RemoteCanceler = %d, want 501", code)
	}
	if _, ok := describe(plain.URL)["cancel"]; ok {
		t.Fatal("describe announces cancel for a source without it")
	}
	if m := Manifest(Module{RemoteSource: &echoSource{}}); m.Has(wickplugin.CapRemoteCancel) {
		t.Fatal("manifest declares remote_cancel without RemoteCanceler")
	}

	src := &cancelSource{}
	srv := httptest.NewServer(Handler(Module{Meta: Meta{Key: "echo"}, RemoteSource: src}, nil))
	defer srv.Close()
	if code := post(srv.URL); code != http.StatusNoContent {
		t.Fatalf("cancel = %d, want 204", code)
	}
	if len(src.got) != 1 || src.got[0] != "h1" {
		t.Fatalf("cancelled = %v", src.got)
	}
	if describe(srv.URL)["cancel"] != true {
		t.Fatal("describe does not announce cancel")
	}
	if m := Manifest(Module{RemoteSource: src}); !m.Has(wickplugin.CapRemoteCancel) {
		t.Fatal("manifest misses remote_cancel")
	}
}

// fieldsSource is echoSource that declares new-session fields, defaults
// taken from the agent's config.
type fieldsSource struct{ echoSource }

func (s *fieldsSource) SessionFields(agentCfg map[string]string) []SessionField {
	return []SessionField{{Key: "branch", Label: "Branch", Placeholder: "main", Default: agentCfg["branch"]}}
}

func TestRemoteSessionFieldsRoute(t *testing.T) {
	fields := func(url string) []SessionField {
		resp, err := http.Post(url+wickplugin.RemotePathSessionFields, "application/json",
			strings.NewReader(`{"agent_id":"a1","config":{"branch":"dev"}}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct{ Fields []SessionField }
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out.Fields
	}
	plain := httptest.NewServer(Handler(Module{Meta: Meta{Key: "echo"}, RemoteSource: &echoSource{}}, nil))
	defer plain.Close()
	if f := fields(plain.URL); len(f) != 0 {
		t.Fatalf("fields without RemoteSessionFielder = %v", f)
	}
	if m := Manifest(Module{RemoteSource: &echoSource{}}); m.Has(wickplugin.CapRemoteSessionFields) {
		t.Fatal("manifest declares remote_session_fields without RemoteSessionFielder")
	}

	srv := httptest.NewServer(Handler(Module{Meta: Meta{Key: "echo"}, RemoteSource: &fieldsSource{}}, nil))
	defer srv.Close()
	f := fields(srv.URL)
	if len(f) != 1 || f[0].Key != "branch" || f[0].Default != "dev" {
		t.Fatalf("fields = %+v", f)
	}
	resp, err := http.Get(srv.URL + wickplugin.RemotePathDescribe)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["session_fields"] != true {
		t.Fatal("describe does not announce session_fields")
	}
	if m := Manifest(Module{RemoteSource: &fieldsSource{}}); !m.Has(wickplugin.CapRemoteSessionFields) {
		t.Fatal("manifest misses remote_session_fields")
	}
}

func TestManifestCarriesAutoOff(t *testing.T) {
	if m := Manifest(Module{Meta: Meta{Key: "x"}}); m.AutoOff != nil {
		t.Fatalf("zero AutoOff = %+v, want nil", m.AutoOff)
	}
	m := Manifest(Module{Meta: Meta{Key: "x"}, AutoOff: AutoOff{Supported: true, DefaultIdle: 20 * time.Minute}})
	if m.AutoOff == nil || !m.AutoOff.Supported || m.AutoOff.DefaultIdleSeconds != 1200 {
		t.Fatalf("AutoOff = %+v", m.AutoOff)
	}
	m = Manifest(Module{AutoOff: AutoOff{Reason: "polls a queue"}})
	if m.AutoOff == nil || m.AutoOff.Supported || m.AutoOff.Reason != "polls a queue" {
		t.Fatalf("AutoOff = %+v", m.AutoOff)
	}
}
