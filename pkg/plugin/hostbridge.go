package plugin

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// HostBrokerID is the GRPCBroker service id wick serves the Host API on. It
// is fixed rather than announced, so neither the handshake nor the plugin
// protos change: the SDK dials it right away, and on a wick that never
// serves it the dial times out and the SDK falls back to the HTTP door (or
// ErrHostAPIUnavailable).
const HostBrokerID uint32 = 0x77696b01

// EnvHostBroker is set ("1") on a process wick serves the Host API to over
// the broker; the SDK only dials when it is present, so a plugin under an
// older wick never waits on a dial that cannot succeed.
const EnvHostBroker = "WICK_HOST_API_BROKER"

// HostBridge is wick's side of the Host API for go-plugin processes. wick
// sets one at start (SetHostBridge); with none, plugins run without a Host
// API exactly as before.
type HostBridge interface {
	// GRPCServer builds the Host API server the broker serves to one process
	// of pluginKey.
	GRPCServer(pluginKey, kind string) func([]grpc.ServerOption) *grpc.Server
	// BeginCall records who one request to pluginKey runs for and returns
	// the call id the plugin echoes back on its Host API calls; end forgets
	// it once the request is over.
	BeginCall(pluginKey, kind, userID, sessionID string) (callID string, end func())
	// ProcessEnv is the Host API env of a new process of pluginKey: the
	// private HTTP door address and a fresh plugin token. release revokes
	// that token; call it once the process is gone.
	ProcessEnv(pluginKey, kind string) (env []string, release func())
}

var (
	bridgeMu sync.RWMutex
	bridge   HostBridge
)

// SetHostBridge installs wick's Host API bridge (nil removes it).
func SetHostBridge(b HostBridge) {
	bridgeMu.Lock()
	bridge = b
	bridgeMu.Unlock()
}

func currentBridge() HostBridge {
	bridgeMu.RLock()
	defer bridgeMu.RUnlock()
	return bridge
}

// HostProcessEnv returns the Host API env for a new process of key (nil
// when wick runs no Host API) and the release that revokes its token once
// the process is gone (never nil).
func HostProcessEnv(key, kind string) ([]string, func()) {
	if b := currentBridge(); b != nil && key != "" {
		env, release := b.ProcessEnv(key, kind)
		if release == nil {
			release = func() {}
		}
		return env, release
	}
	return nil, func() {}
}

// serveHostBroker serves the Host API to one plugin process (host side).
// The server stops with the broker, i.e. when the process goes away.
func serveHostBroker(broker *goplugin.GRPCBroker, key, kind string) {
	b := currentBridge()
	if b == nil || broker == nil || key == "" {
		return
	}
	go broker.AcceptAndServe(HostBrokerID, b.GRPCServer(key, kind))
}

// BeginHostCall records who one HTTP request about to be proxied to plugin
// key runs for (source: "caller" for a session, "token" for the owner of an
// access token) and returns the call id to send as host.HeaderCallID; end
// forgets it when the request is over. No bridge or no person = ("", no-op).
func BeginHostCall(key, kind, userID, source, sessionID string) (string, func()) {
	b := currentBridge()
	if b == nil || key == "" {
		return "", func() {}
	}
	if bs, ok := b.(interface {
		BeginCallFrom(key, kind, userID, source, sessionID string) (string, func())
	}); ok {
		return bs.BeginCallFrom(key, kind, userID, source, sessionID)
	}
	return b.BeginCall(key, kind, userID, sessionID)
}

// beginHostCall stamps a call id for one request to key on ctx's outgoing
// metadata (host side). end must be called when the request is over.
func beginHostCall(ctx context.Context, key, kind, userID, sessionID string) (context.Context, func()) {
	return beginHostCallFor(ctx, key, kind, userID, sessionID, "")
}

// beginHostCallFor is beginHostCall that also records the connector
// instance the request executes, so instance-bound Host API calls
// (ConnectedAccounts) can be held to it. A bridge without
// BeginCallInstance records no instance.
func beginHostCallFor(ctx context.Context, key, kind, userID, sessionID, instanceID string) (context.Context, func()) {
	b := currentBridge()
	if b == nil || key == "" {
		return ctx, func() {}
	}
	var id string
	var end func()
	if bi, ok := b.(interface {
		BeginCallInstance(key, kind, userID, sessionID, instanceID string) (string, func())
	}); ok {
		id, end = bi.BeginCallInstance(key, kind, userID, sessionID, instanceID)
	} else {
		id, end = b.BeginCall(key, kind, userID, sessionID)
	}
	if id == "" {
		return ctx, end
	}
	return metadata.AppendToOutgoingContext(ctx, host.MetadataCallID, id), end
}

// dialHostBroker starts dialling wick's Host API on broker (plugin side) and
// hands the pending connection to host.Default. go-plugin drops the address
// wick announces after a few seconds unless it is dialled, so this runs as
// soon as the plugin serves, not on first use.
func dialHostBroker(broker *goplugin.GRPCBroker) {
	if broker == nil || os.Getenv(EnvHostBroker) != "1" {
		return
	}
	done := make(chan struct{})
	var cc *grpc.ClientConn
	var err error
	go func() {
		defer close(done)
		cc, err = broker.Dial(HostBrokerID)
	}()
	host.SetConnFunc(func() (grpc.ClientConnInterface, error) {
		<-done
		if err != nil {
			return nil, err
		}
		return cc, nil
	})
}

// hostRetry is how long a failed Host probe is remembered before the next
// call probes again (a plugin started before wick's Host API was up).
const hostRetry = 30 * time.Second

var hostOnce struct {
	sync.Mutex
	h   host.Host
	err error
	ok  bool
	gen uint64
	at  time.Time
}

// Host returns wick's Host API for this plugin process: the broker
// connection for connector/tool/job plugins, else the HTTP door from the
// env. It probes Whoami once; a wick without a Host API (no transport, or
// the callback door's version 0) yields host.ErrHostAPIUnavailable, and the
// plugin should degrade.
func Host(ctx context.Context) (host.Host, error) {
	hostOnce.Lock()
	defer hostOnce.Unlock()
	gen := host.ConnGeneration()
	if hostOnce.ok && hostOnce.gen == gen && (hostOnce.err == nil || time.Since(hostOnce.at) < hostRetry) {
		return hostOnce.h, hostOnce.err
	}
	h, err := host.Default()
	if err == nil {
		var who *hostpb.WhoamiResponse
		who, err = h.Whoami(ctx)
		if err == nil && who.GetHostApiVersion() == 0 {
			err = host.ErrHostAPIUnavailable
		}
		if err != nil && ctx.Err() != nil {
			return nil, err // the caller gave up; probe again next time
		}
	}
	if err != nil {
		h = nil
	}
	hostOnce.h, hostOnce.err, hostOnce.ok, hostOnce.gen, hostOnce.at = h, err, true, gen, time.Now()
	return h, err
}

// userNames caches GetUser display names for connector.Ctx.UserName.
var userNames = struct {
	sync.Mutex
	m map[string]userName
}{m: map[string]userName{}}

type userName struct {
	name string
	ok   bool
	at   time.Time
}

const (
	userNameTTL   = 5 * time.Minute
	userNameLimit = 512
)

// hostUserName resolves a wick user id to a display name through the Host
// API, for plugin-side connector.Ctx.UserName. No Host API = ("", false),
// which is what a plugin got before.
func hostUserName(ctx context.Context) func(string) (string, bool) {
	return func(id string) (string, bool) {
		userNames.Lock()
		if e, ok := userNames.m[id]; ok && time.Since(e.at) < userNameTTL {
			userNames.Unlock()
			return e.name, e.ok
		}
		userNames.Unlock()
		h, err := Host(ctx)
		if err != nil {
			return "", false
		}
		u, err := h.GetUser(ctx, &hostpb.GetUserRequest{Id: id})
		if err != nil && status.Code(err) != codes.NotFound && !errors.As(err, new(*host.PermissionDeniedError)) {
			return "", false // transient: do not cache
		}
		e := userName{at: time.Now()}
		if err == nil {
			e.name = u.GetName()
			if e.name == "" {
				e.name = u.GetEmail()
			}
			e.ok = e.name != ""
		}
		userNames.Lock()
		if len(userNames.m) >= userNameLimit {
			clear(userNames.m)
		}
		userNames.m[id] = e
		userNames.Unlock()
		return e.name, e.ok
	}
}

// hostAccounts lists instanceID's connected accounts through the Host API,
// for plugin-side connector.Ctx.ConnectedAccounts. Like the host, it only
// queries when an op asks. No Host API or no instance = nil, which is what a
// plugin got before.
func hostAccounts(ctx context.Context, instanceID string) func() []connector.AccountRef {
	return func() []connector.AccountRef {
		if instanceID == "" {
			return nil
		}
		h, err := Host(ctx)
		if err != nil {
			return nil
		}
		resp, err := h.ConnectedAccounts(ctx, &hostpb.ConnectedAccountsRequest{InstanceId: instanceID})
		if err != nil {
			return nil
		}
		out := make([]connector.AccountRef, 0, len(resp.GetAccounts()))
		for _, a := range resp.GetAccounts() {
			out = append(out, connector.AccountRef{ID: a.GetId(), DisplayName: a.GetDisplayName(), WickUserID: a.GetWickUserId()})
		}
		return out
	}
}
