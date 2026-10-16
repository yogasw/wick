package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// rawBody decodes a request body with encoding/json alone, the way a plugin
// without stubs (or wick's server) sees it.
func rawBody(t *testing.T, r *http.Request) map[string]json.RawMessage {
	t.Helper()
	b, _ := io.ReadAll(r.Body)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Errorf("body is not a JSON object: %s", b)
	}
	return m
}

func writeRaw(w http.ResponseWriter, body string) {
	w.Header().Set(host.HeaderHostAPI, "1")
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func serve(t *testing.T, mux *http.ServeMux) host.Host {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	h, err := host.FromHTTP(srv.URL, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHTTPInvokeAndExecuteCarryRawJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /x/-/api/invoke", func(w http.ResponseWriter, r *http.Request) {
		m := rawBody(t, r)
		if string(m["method"]) != `"users.list"` || string(m["args"]) != `{"limit":2}` || m["args_json"] != nil {
			t.Errorf("invoke body = %v", m)
		}
		writeRaw(w, `{"result": {"ok": true, "n": 2}}`)
	})
	mux.HandleFunc("POST /x/-/api/connectors/execute", func(w http.ResponseWriter, r *http.Request) {
		m := rawBody(t, r)
		want := map[string]string{"connector": `"slack"`, "instance_id": `"i1"`, "op": `"lookup_user_by_email"`, "args": `{"email":"a@b.c"}`, "account_id": `"acc"`}
		for k, v := range want {
			if string(m[k]) != v {
				t.Errorf("execute body %s = %s, want %s", k, m[k], v)
			}
		}
		writeRaw(w, `{"result":[1,2],"meta":{"instance":"i1"}}`)
	})
	h := serve(t, mux)
	ctx := context.Background()

	inv, err := h.Invoke(ctx, &hostpb.InvokeRequest{Method: "users.list", ArgsJson: []byte(`{"limit": 2}`)})
	if err != nil || string(inv.GetResultJson()) != `{"ok":true,"n":2}` {
		t.Fatalf("Invoke = %s, %v", inv.GetResultJson(), err)
	}
	ex, err := h.ExecuteConnector(ctx, &hostpb.ExecuteConnectorRequest{Connector: "slack", InstanceId: "i1", Op: "lookup_user_by_email", ArgsJson: []byte(`{"email":"a@b.c"}`), AccountId: "acc"})
	if err != nil || string(ex.GetResultJson()) != `[1,2]` || ex.GetMeta()["instance"] != "i1" {
		t.Fatalf("ExecuteConnector = %v, %v", ex, err)
	}
	if _, err := h.Invoke(ctx, &hostpb.InvokeRequest{Method: "x", ArgsJson: []byte(`{bad`)}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid args JSON: want InvalidArgument, got %v", err)
	}
}

func TestCodecQueryAndFallbacks(t *testing.T) {
	b, err := host.MarshalHTTP(&hostpb.QueryRequest{Sql: "select 1", Args: []string{"a"}, MaxRows: 5, TimeoutMs: 100, Write: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"max_rows":5,"params":["a"],"sql":"select 1","timeout_ms":100,"write":true}` {
		t.Fatalf("query body = %s", b)
	}
	var qr hostpb.QueryResponse
	if err := host.UnmarshalHTTP([]byte(`{"columns":["c"],"rows":[{"values":["x"],"nulls":[false]}],"rows_affected":7,"truncated":true,"extra":1}`), &qr); err != nil {
		t.Fatal(err)
	}
	if qr.GetRowsAffected() != 7 || qr.GetRows()[0].GetValues()[0] != "x" || !qr.GetTruncated() {
		t.Fatalf("query response = %v", &qr)
	}
	out, _ := host.MarshalHTTP(&qr)
	if string(out) != `{"columns":["c"],"rows":[{"nulls":[false],"values":["x"]}],"rows_affected":7,"truncated":true}` {
		t.Fatalf("int64 must be a JSON number: %s", out)
	}
	// protojson-style base64 under the proto name is still accepted.
	var inv hostpb.InvokeRequest
	if err := host.UnmarshalHTTP([]byte(`{"method":"m","args_json":"eyJhIjoxfQ=="}`), &inv); err != nil || string(inv.GetArgsJson()) != `{"a":1}` {
		t.Fatalf("base64 fallback = %s, %v", inv.GetArgsJson(), err)
	}
}

// GetConfig's withheld flag survives the HTTP door both ways.
func TestHTTPGetConfigWithheld(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x/-/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("owner") != "job" || r.URL.Query().Get("key") != "token" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		body, _ := host.MarshalHTTP(&hostpb.GetConfigResponse{Found: true, Secret: true, Withheld: true})
		if !strings.Contains(string(body), `"withheld":true`) {
			t.Errorf("encoded = %s", body)
		}
		writeRaw(w, string(body))
	})
	got, err := serve(t, mux).GetConfig(context.Background(), &hostpb.GetConfigRequest{Owner: "job", Key: "token"})
	if err != nil || !got.GetWithheld() || !got.GetFound() || !got.GetSecret() || got.GetValue() != "" {
		t.Fatalf("GetConfig = %v, %v", got, err)
	}
}

func TestHTTPUpdateTicketOptionalFields(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /x/-/api/tickets/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(b, &raw)
		if _, ok := raw["status"]; !ok {
			t.Errorf("explicitly empty status dropped: %s", b)
		}
		if _, ok := raw["title"]; ok {
			t.Errorf("unset title sent: %s", b)
		}
		var in hostpb.UpdateTicketRequest
		if err := host.UnmarshalHTTP(b, &in); err != nil {
			t.Fatal(err)
		}
		if in.Status == nil || in.GetStatus() != "" || in.Title != nil || !in.GetSetAssignees() || in.GetFields()["k"] != "v" {
			t.Errorf("decoded = %v", &in)
		}
		writeRaw(w, `{"id":"`+r.PathValue("id")+`","status":""}`)
	})
	h := serve(t, mux)
	tk, err := h.UpdateTicket(context.Background(), &hostpb.UpdateTicketRequest{
		Id: "T-1", Status: proto.String(""), SetAssignees: true, Fields: map[string]string{"k": "v"},
	})
	if err != nil || tk.GetId() != "T-1" {
		t.Fatalf("UpdateTicket = %v, %v", tk, err)
	}
}

func TestHTTPWhoamiSuperset(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x/-/api/whoami", func(w http.ResponseWriter, _ *http.Request) {
		writeRaw(w, `{"plugin":"demo","scopes":["users.read"],"plugin_key":"demo","kind":"service","scopes_allowed":["users.read"],"host_api_version":1}`)
	})
	who, err := serve(t, mux).Whoami(context.Background())
	if err != nil || who.GetPluginKey() != "demo" || who.GetHostApiVersion() != 1 || who.GetKind() != "service" {
		t.Fatalf("Whoami = %v, %v", who, err)
	}
}

func TestHTTPErrorMapping(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x/-/api/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") {
		case "precondition":
			host.WriteHTTPError(w, status.Error(codes.FailedPrecondition, "no user identity — set wick_pat or run on behalf of a user"))
		case "unimplemented":
			host.WriteHTTPError(w, host.ErrHostAPIUnavailable)
		case "canceled":
			host.WriteHTTPError(w, context.Canceled)
		case "bare":
			// Marker header, no ErrorBody: the status alone decides.
			w.Header().Set(host.HeaderHostAPI, "1")
			w.WriteHeader(http.StatusNotFound)
		}
	})
	h := serve(t, mux)
	get := func(id string) error {
		_, err := h.GetUser(context.Background(), &hostpb.GetUserRequest{Id: id})
		return err
	}
	if err := get("precondition"); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("precondition: %v", err)
	}
	// 501 with the marker = the host serves the Host API but not this RPC:
	// same meaning as gRPC Unimplemented, so the same ErrHostAPIUnavailable.
	if err := get("unimplemented"); !errors.Is(err, host.ErrHostAPIUnavailable) {
		t.Errorf("unimplemented: %v", err)
	}
	if err := get("canceled"); status.Code(err) != codes.Canceled {
		t.Errorf("canceled: %v", err)
	}
	if err := get("bare"); status.Code(err) != codes.NotFound {
		t.Errorf("bare 404: %v", err)
	}
	rec := httptest.NewRecorder()
	host.WriteHTTPError(rec, status.Error(codes.FailedPrecondition, "x"))
	if rec.Code != http.StatusPreconditionFailed || rec.Header().Get(host.HeaderHostAPIError) != "FailedPrecondition" {
		t.Errorf("WriteHTTPError = %d %v", rec.Code, rec.Header())
	}
}

func TestHTTPContextCanceled(t *testing.T) {
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x/-/api/users/{id}", func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	h := serve(t, mux)
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	if _, err := h.GetUser(ctx, &hostpb.GetUserRequest{Id: "u"}); status.Code(err) != codes.Canceled {
		t.Fatalf("want Canceled, got %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.GetUser(ctx, &hostpb.GetUserRequest{Id: "u"}); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
}

func TestHTTPRestPassthrough(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/x/-/api/rest/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(host.HeaderHostAPI, "1")
		w.Header().Set("X-Seen-Accept", r.Header.Get("Accept"))
		w.Header().Set("X-Seen-User", r.Header.Get("X-Wick-User-Id"))
		switch r.URL.Path {
		case "/x/-/api/rest/api/moved":
			w.Header().Set("Location", "/api/elsewhere")
			w.WriteHeader(http.StatusFound)
		case "/x/-/api/rest/api/big":
			_, _ = io.WriteString(w, "0123456789ABCDEF")
		default:
			_, _ = io.WriteString(w, "ok")
		}
	})
	h := serve(t, mux)
	ctx := context.Background()

	rr, err := h.Rest(ctx, &hostpb.RestRequest{Path: "/api/moved"})
	if err != nil || rr.GetStatus() != http.StatusFound || rr.GetHeaders()["Location"] != "/api/elsewhere" {
		t.Fatalf("redirect must reach the caller: %v, %v", rr, err)
	}
	rr, err = h.Rest(ctx, &hostpb.RestRequest{Path: "/api/x", Headers: map[string]string{"Accept": "text/csv", "X-Wick-User-Id": "admin"}})
	if err != nil || rr.GetHeaders()["X-Seen-Accept"] != "text/csv" || rr.GetHeaders()["X-Seen-User"] != "" {
		t.Fatalf("headers: %v, %v", rr.GetHeaders(), err)
	}
	if _, ok := rr.GetHeaders()[host.HeaderHostAPI]; ok {
		t.Fatal("marker header leaked into RestResponse")
	}

	defer host.SetMaxResponseBytes(10)()
	if _, err := h.Rest(ctx, &hostpb.RestRequest{Path: "/api/big"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized body: want ResourceExhausted, got %v", err)
	}
}

func TestFromEnvPrefersHostAPIURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x/-/api/whoami", func(w http.ResponseWriter, _ *http.Request) {
		writeRaw(w, `{"plugin_key":"demo","host_api_version":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	host.SetConn(nil)
	t.Setenv(host.EnvBaseURL, "http://127.0.0.1:1") // public URL: must not be used
	t.Setenv(host.EnvHostAPIURL, srv.URL)
	t.Setenv(host.EnvPluginToken, token)
	h, err := host.Default()
	if err != nil {
		t.Fatal(err)
	}
	if who, err := h.Whoami(context.Background()); err != nil || who.GetHostApiVersion() != 1 {
		t.Fatalf("Whoami via WICK_HOST_API_URL = %v, %v", who, err)
	}
}

// deadlineHost fails a call that arrives without a deadline.
type deadlineHost struct{ hostpb.UnimplementedHostServer }

func (deadlineHost) Whoami(ctx context.Context, _ *hostpb.WhoamiRequest) (*hostpb.WhoamiResponse, error) {
	d, ok := ctx.Deadline()
	if !ok || time.Until(d) > host.DefaultCallTimeout {
		return nil, status.Error(codes.FailedPrecondition, "no default deadline")
	}
	return &hostpb.WhoamiResponse{PluginKey: "demo"}, nil
}

func TestGRPCDefaultDeadline(t *testing.T) {
	cc := dialBufconn(t, func(s *grpc.Server) { hostpb.RegisterHostServer(s, deadlineHost{}) })
	h, _ := host.FromConn(cc)
	if _, err := h.Whoami(context.Background()); err != nil {
		t.Fatal(err)
	}
}
