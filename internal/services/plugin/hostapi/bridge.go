package hostapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// grpcMaxMsg lets a Rest body (16 MiB) and response (64 MiB) through the
// broker transport with room for the envelope.
const grpcMaxMsg = 80 << 20

// tokensPerKey is how many live Host API tokens one plugin key keeps: one
// per running process (a job can overlap itself), the oldest dropped first.
const tokensPerKey = 4

// ServiceTokens is the service host's callback token store: service plugins
// keep their scoped WICK_PLUGIN_TOKEN, and the door accepts it too.
type ServiceTokens interface {
	LookupCallback(bearer string) (key string, scopes []string, ok bool)
	CallbackRevoked(key string) bool
}

// Bridge carries the Server to plugin processes: the gRPC door over each
// go-plugin broker, the private HTTP door, the per-process tokens and the
// call ids that tie a Host API call to the person behind the request.
// It satisfies wickplugin.HostBridge.
type Bridge struct {
	srv     *Server
	door    http.Handler
	service ServiceTokens

	mu     sync.Mutex
	calls  map[string]Caller
	tokens map[string]tokenEntry // sha256(token) → process
	issued map[string][]string   // plugin key → token hashes, oldest first
	ln     net.Listener
	hs     *http.Server
	url    string
}

type tokenEntry struct{ key, kind string }

var _ wickplugin.HostBridge = (*Bridge)(nil)

// NewBridge returns a Bridge over srv. service may be nil (no service
// plugins).
func NewBridge(srv *Server, service ServiceTokens) *Bridge {
	return &Bridge{
		srv: srv, door: HTTPHandler(srv), service: service,
		calls: map[string]Caller{}, tokens: map[string]tokenEntry{}, issued: map[string][]string{},
	}
}

// Start opens the private HTTP door on a loopback port. Plugins learn the
// address from WICK_HOST_API_URL; wick's public URL never takes a plugin
// token.
func (b *Bridge) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	hs := &http.Server{Handler: b, ReadHeaderTimeout: 10 * time.Second}
	b.mu.Lock()
	b.ln, b.hs, b.url = ln, hs, "http://"+ln.Addr().String()
	b.mu.Unlock()
	go func() { _ = hs.Serve(ln) }()
	return nil
}

// Close stops the HTTP door.
func (b *Bridge) Close() error {
	b.mu.Lock()
	hs := b.hs
	b.hs, b.url = nil, ""
	b.mu.Unlock()
	if hs == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return hs.Shutdown(ctx)
}

// URL is the private door address ("" before Start).
func (b *Bridge) URL() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.url
}

func newSecret(prefix string) string {
	var buf [24]byte
	_, _ = rand.Read(buf[:])
	return prefix + hex.EncodeToString(buf[:])
}

func hashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ProcessEnv is the Host API env of a new connector/tool/job process: the
// private door, the broker flag and a fresh token (none while an admin has
// revoked the plugin's callback access). release revokes that token; the
// spawner calls it once the process is gone.
func (b *Bridge) ProcessEnv(key, kind string) ([]string, func()) {
	env := []string{wickplugin.EnvHostBroker + "=1"}
	if u := b.URL(); u != "" {
		env = append(env, host.EnvHostAPIURL+"="+u)
	}
	if b.revoked(key) {
		return env, func() {}
	}
	tok := newSecret("wick_hst_")
	h := hashSecret(tok)
	b.mu.Lock()
	b.tokens[h] = tokenEntry{key: key, kind: kind}
	list := append(b.issued[key], h)
	for len(list) > tokensPerKey {
		delete(b.tokens, list[0])
		list = list[1:]
	}
	b.issued[key] = list
	b.mu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { b.dropToken(key, h) }) }
	return append(env, host.EnvPluginToken+"="+tok), release
}

// dropToken revokes one process token of key.
func (b *Bridge) dropToken(key, h string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.tokens, h)
	list := b.issued[key]
	for i, x := range list {
		if x == h {
			b.issued[key] = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	if len(b.issued[key]) == 0 {
		delete(b.issued, key)
	}
}

// RevokeKey drops every live process token of key at once (the admin's
// "Callback to wick" revoke for any kind). Broker calls are refused through
// the same CallbackRevoked flag the door checks.
func (b *Bridge) RevokeKey(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, h := range b.issued[key] {
		delete(b.tokens, h)
	}
	delete(b.issued, key)
}

func (b *Bridge) revoked(key string) bool {
	return b.service != nil && b.service.CallbackRevoked(key)
}

// BeginCall records that the request about to be sent to plugin key runs for
// userID (from sessionID); the plugin echoes the returned id on its Host API
// calls. Nothing to record yields "".
func (b *Bridge) BeginCall(key, kind, userID, sessionID string) (string, func()) {
	return b.BeginCallFrom(key, kind, userID, SourceCaller, sessionID)
}

// BeginCallFrom is BeginCall with the identity's source (SourceToken for a
// service route reached with an access token).
func (b *Bridge) BeginCallFrom(key, kind, userID, source, sessionID string) (string, func()) {
	return b.beginCall(key, kind, userID, source, sessionID, "")
}

// BeginCallInstance is BeginCall for a connector Execute: it also records
// the instance being executed, which instance-bound RPCs are held to.
func (b *Bridge) BeginCallInstance(key, kind, userID, sessionID, instanceID string) (string, func()) {
	return b.beginCall(key, kind, userID, SourceCaller, sessionID, instanceID)
}

func (b *Bridge) beginCall(key, kind, userID, source, sessionID, instanceID string) (string, func()) {
	if key == "" || (userID == "" && sessionID == "" && instanceID == "") {
		return "", func() {}
	}
	if userID == "" {
		source = ""
	}
	id := newSecret("")
	b.mu.Lock()
	b.calls[id] = Caller{PluginKey: key, Kind: kind, UserID: userID, Source: source, SessionID: sessionID, InstanceID: instanceID}
	b.mu.Unlock()
	var once sync.Once
	return id, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.calls, id)
			b.mu.Unlock()
		})
	}
}

// errUnknownCall refuses a call id wick did not issue to this plugin or whose
// request is over: falling back to wick_pat would quietly run the call as
// someone else.
var errUnknownCall = status.Error(codes.Unauthenticated,
	"unknown or expired call id (the wick request it belonged to is over, or it was issued to another plugin)")

// caller is the identity of one Host API call from plugin key: the plugin
// alone with no callID, the person behind callID when wick issued it to this
// same plugin and the request is still running, else errUnknownCall.
func (b *Bridge) caller(key, kind, callID string) (Caller, error) {
	c := Caller{PluginKey: key, Kind: kind}
	if callID == "" {
		return c, nil
	}
	b.mu.Lock()
	got, ok := b.calls[callID]
	b.mu.Unlock()
	if !ok || got.PluginKey != key {
		return Caller{}, errUnknownCall
	}
	c.UserID, c.Source, c.SessionID, c.InstanceID = got.UserID, got.Source, got.SessionID, got.InstanceID
	return c, nil
}

// GRPCServer is the broker-side Host API server for one process of key.
func (b *Bridge) GRPCServer(key, kind string) func([]grpc.ServerOption) *grpc.Server {
	return func(opts []grpc.ServerOption) *grpc.Server {
		opts = append(opts,
			grpc.MaxRecvMsgSize(grpcMaxMsg), grpc.MaxSendMsgSize(grpcMaxMsg),
			grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
				id := ""
				if md, ok := metadata.FromIncomingContext(ctx); ok {
					if v := md.Get(host.MetadataCallID); len(v) > 0 {
						id = v[0]
					}
				}
				if b.revoked(key) {
					return nil, status.Error(codes.Unauthenticated, "plugin callback access revoked")
				}
				c, err := b.caller(key, kind, id)
				if err != nil {
					return nil, err
				}
				return next(WithCaller(ctx, c), req)
			}))
		s := grpc.NewServer(opts...)
		hostpb.RegisterHostServer(s, b.srv)
		return s
	}
}

// authenticate maps a door bearer to its plugin: a connector/tool/job process
// token, else a service plugin's callback token.
func (b *Bridge) authenticate(bearer string) (key, kind string, scopes []string, ok bool) {
	if bearer == "" {
		return "", "", nil, false
	}
	b.mu.Lock()
	e, found := b.tokens[hashSecret(bearer)]
	b.mu.Unlock()
	if found {
		if b.revoked(e.key) {
			return "", "", nil, false
		}
		return e.key, e.kind, nil, true
	}
	if b.service != nil {
		if key, scopes, ok := b.service.LookupCallback(bearer); ok {
			return key, wickplugin.KindService, scopes, true
		}
	}
	return "", "", nil, false
}

// ServeHTTP is the private HTTP door: plugin token → Caller → the shared
// handler.
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	key, kind, scopes, ok := b.authenticate(strings.TrimSpace(tok))
	if !ok {
		host.WriteHTTPError(w, status.Error(codes.Unauthenticated, "invalid or revoked plugin token"))
		return
	}
	b.ServeAs(w, r, key, kind, scopes)
}

// ServeAs serves r for a plugin the caller already authenticated (the public
// callback door's whoami).
func (b *Bridge) ServeAs(w http.ResponseWriter, r *http.Request, key, kind string, scopes []string) {
	c, err := b.caller(key, kind, r.Header.Get(host.HeaderCallID))
	if err != nil {
		host.WriteHTTPError(w, err)
		return
	}
	c.CallbackScopes = scopes
	r.Header.Del("Authorization")
	b.door.ServeHTTP(w, r.WithContext(WithCaller(r.Context(), c)))
}
