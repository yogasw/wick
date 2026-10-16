package host_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// fakeHost implements a handful of RPCs; the rest answer Unimplemented.
type fakeHost struct {
	hostpb.UnimplementedHostServer
}

func (fakeHost) Whoami(context.Context, *hostpb.WhoamiRequest) (*hostpb.WhoamiResponse, error) {
	return &hostpb.WhoamiResponse{PluginKey: "demo", Kind: "tool", HostApiVersion: host.APIVersion}, nil
}

func (fakeHost) GetUser(_ context.Context, in *hostpb.GetUserRequest) (*hostpb.User, error) {
	switch in.GetId() {
	case "u1":
		return &hostpb.User{Id: "u1", Name: "Ada", Email: "ada@example.com"}, nil
	case "secret":
		return nil, &host.PermissionDeniedError{Scope: "users.read", Item: "secret", Plugin: "demo", Message: "blocked by admin"}
	}
	return nil, status.Error(codes.NotFound, "no such user")
}

func (fakeHost) AttachSession(context.Context, *hostpb.AttachSessionRequest) (*hostpb.Empty, error) {
	return &hostpb.Empty{}, nil
}

func dialBufconn(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	cc, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return cc
}

func TestGRPCClient(t *testing.T) {
	cc := dialBufconn(t, func(s *grpc.Server) { hostpb.RegisterHostServer(s, fakeHost{}) })
	h, err := host.FromConn(cc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	who, err := h.Whoami(ctx)
	if err != nil || who.GetPluginKey() != "demo" || who.GetHostApiVersion() != 1 {
		t.Fatalf("Whoami = %v, %v", who, err)
	}
	u, err := h.GetUser(ctx, &hostpb.GetUserRequest{Id: "u1"})
	if err != nil || u.GetEmail() != "ada@example.com" {
		t.Fatalf("GetUser = %v, %v", u, err)
	}
	if err := h.AttachSession(ctx, &hostpb.AttachSessionRequest{TicketId: "t", SessionId: "s"}); err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	_, err = h.GetUser(ctx, &hostpb.GetUserRequest{Id: "secret"})
	var pd *host.PermissionDeniedError
	if !errors.As(err, &pd) || !errors.Is(err, host.ErrPermissionDenied) {
		t.Fatalf("want PermissionDeniedError, got %T %v", err, err)
	}
	if pd.Scope != "users.read" || pd.Item != "secret" || pd.Plugin != "demo" || pd.Message != "blocked by admin" {
		t.Fatalf("details lost: %+v", pd)
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status.Code = %v", status.Code(err))
	}

	if _, err = h.GetUser(ctx, &hostpb.GetUserRequest{Id: "nobody"}); status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
	// An RPC this host does not implement yet.
	if _, err = h.ListTickets(ctx, nil); !errors.Is(err, host.ErrHostAPIUnavailable) {
		t.Fatalf("want ErrHostAPIUnavailable, got %v", err)
	}
}

func TestGRPCClientOldHost(t *testing.T) {
	// A broker connection to a process that never registered the Host service.
	cc := dialBufconn(t, func(*grpc.Server) {})
	h, _ := host.FromConn(cc)
	if _, err := h.Whoami(context.Background()); !errors.Is(err, host.ErrHostAPIUnavailable) {
		t.Fatalf("want ErrHostAPIUnavailable, got %v", err)
	}
}

const token = "tok-123"

func writeProto(t *testing.T, w http.ResponseWriter, m proto.Message) {
	t.Helper()
	b, err := host.MarshalHTTP(m)
	if err != nil {
		t.Fatal(err)
	}
	w.Header().Set(host.HeaderHostAPI, "1")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func newHTTPHost(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				// Same shape the callback door answers today: plain 401.
				http.Error(w, "invalid or revoked plugin token", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /x/-/api/users/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "secret" {
			host.WriteHTTPError(w, &host.PermissionDeniedError{Scope: "users.read", Item: "secret", Plugin: "demo", Message: "blocked"})
			return
		}
		writeProto(t, w, &hostpb.User{Id: r.PathValue("id"), Name: "Ada"})
	}))
	mux.HandleFunc("GET /x/-/api/users", auth(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("q") != "ad" || q.Get("include_unapproved") != "true" || q.Get("limit") != "5" {
			host.WriteHTTPError(w, status.Errorf(codes.InvalidArgument, "bad query %s", r.URL.RawQuery))
			return
		}
		writeProto(t, w, &hostpb.ListUsersResponse{Users: []*hostpb.User{{Id: "u1"}, {Id: "u2"}}})
	}))
	mux.HandleFunc("POST /x/-/api/tickets", auth(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var in hostpb.CreateTicketRequest
		if err := host.UnmarshalHTTP(raw, &in); err != nil {
			host.WriteHTTPError(w, status.Error(codes.InvalidArgument, err.Error()))
			return
		}
		writeProto(t, w, &hostpb.Ticket{Id: "T-1", Title: in.GetTitle(), ProjectId: in.GetProjectId()})
	}))
	mux.HandleFunc("/x/-/api/rest/", auth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(host.HeaderHostAPI, "1")
		w.Header().Set("X-Seen-Path", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"ticket not found"}`))
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPClient(t *testing.T) {
	srv := newHTTPHost(t)
	h, err := host.FromHTTP(srv.URL+"/", token, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	u, err := h.GetUser(ctx, &hostpb.GetUserRequest{Id: "u 1"})
	if err != nil || u.GetId() != "u 1" {
		t.Fatalf("GetUser = %v, %v", u, err)
	}
	list, err := h.ListUsers(ctx, &hostpb.ListUsersRequest{Q: "ad", IncludeUnapproved: true, Limit: 5})
	if err != nil || len(list.GetUsers()) != 2 {
		t.Fatalf("ListUsers = %v, %v", list, err)
	}
	tk, err := h.CreateTicket(ctx, &hostpb.CreateTicketRequest{ProjectId: "p1", Title: "Hello"})
	if err != nil || tk.GetId() != "T-1" || tk.GetTitle() != "Hello" || tk.GetProjectId() != "p1" {
		t.Fatalf("CreateTicket = %v, %v", tk, err)
	}

	// 403: scope denied keeps its details.
	_, err = h.GetUser(ctx, &hostpb.GetUserRequest{Id: "secret"})
	var pd *host.PermissionDeniedError
	if !errors.As(err, &pd) || pd.Scope != "users.read" || pd.Item != "secret" || pd.Plugin != "demo" {
		t.Fatalf("want PermissionDeniedError with details, got %v", err)
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status.Code = %v", status.Code(err))
	}

	// Missing path parameter is caught client-side.
	if _, err = h.GetUser(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}

	// Rest: the proxied API's status is the result, not an error, and the
	// plugin cannot smuggle its own Authorization header.
	rr, err := h.Rest(ctx, &hostpb.RestRequest{Method: "get", Path: "/api/tickets/42", Headers: map[string]string{"Authorization": "Bearer other"}})
	if err != nil || rr.GetStatus() != 404 || string(rr.GetBody()) != `{"error":"ticket not found"}` {
		t.Fatalf("Rest = %v, %v", rr, err)
	}
	if got := rr.GetHeaders()["X-Seen-Path"]; got != "/x/-/api/rest/api/tickets/42" {
		t.Fatalf("rest path = %q", got)
	}
}

func TestHTTPClientBadToken(t *testing.T) {
	srv := newHTTPHost(t)
	h, _ := host.FromHTTP(srv.URL, "revoked", srv.Client())
	if _, err := h.GetUser(context.Background(), &hostpb.GetUserRequest{Id: "u1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v", err)
	}
}

func TestHTTPClientOldHost(t *testing.T) {
	// Today's callback door: whoami in its original shape, every other path 404.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x/-/api/whoami", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"plugin":"demo","scopes":["users.read"]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h, _ := host.FromHTTP(srv.URL, token, srv.Client())
	ctx := context.Background()

	who, err := h.Whoami(ctx)
	if err != nil || who.GetPluginKey() != "demo" || who.GetHostApiVersion() != 0 || len(who.GetScopesAllowed()) != 1 {
		t.Fatalf("legacy Whoami = %v, %v", who, err)
	}
	if _, err := h.ListUsers(ctx, nil); !errors.Is(err, host.ErrHostAPIUnavailable) {
		t.Fatalf("want ErrHostAPIUnavailable, got %v", err)
	}
	if _, err := h.Rest(ctx, &hostpb.RestRequest{Path: "/api/x"}); !errors.Is(err, host.ErrHostAPIUnavailable) {
		t.Fatalf("Rest: want ErrHostAPIUnavailable, got %v", err)
	}
}

func TestDefaultWithoutHost(t *testing.T) {
	t.Setenv(host.EnvBaseURL, "")
	t.Setenv(host.EnvPluginToken, "")
	host.SetConn(nil)
	if h, err := host.Default(); h != nil || !errors.Is(err, host.ErrHostAPIUnavailable) {
		t.Fatalf("Default = %v, %v", h, err)
	}
	if h, err := host.FromConn(nil); h != nil || !errors.Is(err, host.ErrHostAPIUnavailable) {
		t.Fatalf("FromConn(nil) = %v, %v", h, err)
	}
	t.Setenv(host.EnvBaseURL, "http://127.0.0.1:1")
	if _, err := host.Default(); !errors.Is(err, host.ErrHostAPIUnavailable) {
		t.Fatalf("token missing: %v", err)
	}
	t.Setenv(host.EnvPluginToken, token)
	if h, err := host.Default(); err != nil || h == nil {
		t.Fatalf("env set: %v, %v", h, err)
	}
}

func TestEnvNamesMatchPluginSDK(t *testing.T) {
	if host.EnvBaseURL != plugin.EnvBaseURL || host.EnvPluginToken != plugin.EnvPluginToken {
		t.Fatal("env names drifted from pkg/plugin")
	}
}

func TestStatusMappingRoundTrip(t *testing.T) {
	for _, c := range []codes.Code{
		codes.OK, codes.InvalidArgument, codes.Unauthenticated, codes.PermissionDenied,
		codes.NotFound, codes.Aborted, codes.FailedPrecondition, codes.ResourceExhausted,
		codes.Canceled, codes.Unimplemented, codes.Unavailable, codes.DeadlineExceeded,
	} {
		if got := host.CodeForHTTPStatus(host.HTTPStatus(c)); got != c {
			t.Errorf("%v -> %d -> %v", c, host.HTTPStatus(c), got)
		}
	}
	if host.HTTPStatus(codes.Internal) != http.StatusInternalServerError {
		t.Error("Internal must be 500")
	}
}

func TestRoutesCoverEveryRPC(t *testing.T) {
	have := map[string]bool{"Rest": true}
	for _, r := range host.Routes {
		have[r.RPC] = true
	}
	for _, m := range hostpb.Host_ServiceDesc.Methods {
		if !have[m.MethodName] {
			t.Errorf("RPC %s has no HTTP route", m.MethodName)
		}
	}
}
