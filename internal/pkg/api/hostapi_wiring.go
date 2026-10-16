package api

import (
	"context"
	"net/http"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/appname"
	"github.com/yogasw/wick/internal/connectors"
	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	serviceplugin "github.com/yogasw/wick/internal/services/plugin"
	"github.com/yogasw/wick/internal/services/plugin/hostapi"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// hostAPI is the running plugin Host API: the server, its audit writer and
// the bridge that carries it to plugin processes.
type hostAPI struct {
	bridge *hostapi.Bridge
	audit  *hostapi.Audit
	dir    *hostapi.ManifestDirectory
	admin  *hostapi.Admin
}

// hostAPIDeps is what wick hands the Host API server.
type hostAPIDeps struct {
	db         *gorm.DB
	router     http.Handler
	auth       *login.Service
	pats       hostapi.TokenAuthenticator
	configs    configLookup
	connectors *connectors.Service
	layout     agentconfig.Layout
	service    *serviceplugin.Host
	// send queues a message into a session (SendToSession); nil leaves the
	// RPC Unimplemented.
	send func(ctx context.Context, userID, sessionID, text string) (bool, error)
}

// startHostAPI builds the Host API once, opens its private door and installs
// it as the go-plugin bridge. A failure leaves plugins without a Host API
// (they degrade) rather than stopping wick.
func startHostAPI(ctx context.Context, d hostAPIDeps) *hostAPI {
	dir := hostapi.NewManifestDirectory(func(kind string) ([]wickplugin.Manifest, error) {
		found, err := connplugin.ScanKind(connplugin.KindDir(kind), kind)
		if err != nil {
			return nil, err
		}
		out := make([]wickplugin.Manifest, 0, len(found))
		for _, f := range found {
			m := f.Manifest
			m.Module.Meta.Key = f.Key
			out = append(out, m)
		}
		return out, nil
	})
	dir.ConfigOwner = func(kind, key string) string {
		switch kind {
		case wickplugin.KindService:
			return serviceplugin.ConfigOwner(key)
		case wickplugin.KindTool, wickplugin.KindJob:
			return key
		}
		return "" // connector configs are per instance
	}
	dir.Reload(ctx)
	store := hostapi.NewGormStore(d.db)
	audit := hostapi.NewAudit(store)
	audit.Start(ctx)
	users := hostapi.UserLookup(func(ctx context.Context, id string) (*entity.User, []string, error) {
		u, err := d.auth.GetUserByID(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		return u, d.auth.GetUserFilterTagIDs(ctx, id), nil
	})
	layoutData := &hostapi.LayoutData{Layout: d.layout, Users: users, Send: d.send}
	connRunner := hostapi.NewConnectorRunner(d.connectors)
	perms := hostapi.NewPermissions(store)
	srv := hostapi.NewServer(hostapi.Deps{
		Plugins:     dir,
		Users:       dbUsers{db: d.db},
		UserTags:    userTags(d.db),
		Configs:     ownedConfigs{c: d.configs},
		Accounts:    connectedAccounts{svc: d.connectors},
		PATs:        d.pats,
		Sessions:    layoutData,
		Tickets:     layoutData,
		Principals:  d.auth,
		Connectors:  connRunner,
		Router:      d.router,
		DB:          d.db,
		Perms:       perms,
		Audit:       audit,
		WickVersion: appname.BuildAppVersion,
	})
	var svcTokens hostapi.ServiceTokens
	if d.service != nil && d.service.Tokens != nil {
		svcTokens = d.service.Tokens
	}
	bridge := hostapi.NewBridge(srv, svcTokens)
	if err := bridge.Start(); err != nil {
		log.Warn().Err(err).Msg("host api: private door failed to open; plugins run without it")
		audit.Close()
		return nil
	}
	wickplugin.SetHostBridge(bridge)
	if d.service != nil {
		d.service.HostAPI = bridge
	}
	admin := &hostapi.Admin{Plugins: dir, Perms: perms, Calls: store, Op: connRunner.Op, Revoke: bridge.SetRevoked, Revoked: bridge.Revoked,
		Actor: func(r *http.Request) string {
			if u := login.GetUser(r.Context()); u != nil {
				return u.ID
			}
			return ""
		}}
	return &hostAPI{bridge: bridge, audit: audit, dir: dir, admin: admin}
}

// RegisterAdmin mounts the Host access admin API (Plugins → Host access);
// wrap is the admin middleware. Safe on a nil hostAPI.
func (h *hostAPI) RegisterAdmin(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if h == nil || h.admin == nil {
		// The Host API did not start: the page still answers, with 503.
		hostapi.RegisterUnavailableAdmin(mux, wrap)
		return
	}
	h.admin.RegisterAdmin(mux, wrap)
}

// Close stops the door and flushes the audit.
func (h *hostAPI) Close() {
	if h == nil {
		return
	}
	wickplugin.SetHostBridge(nil)
	_ = h.bridge.Close()
	h.audit.Close()
}

// dbUsers lists wick users for the Host API users cache.
type dbUsers struct{ db *gorm.DB }

func (u dbUsers) ListUsers(ctx context.Context) ([]*entity.User, error) {
	var out []*entity.User
	return out, u.db.WithContext(ctx).Order("email").Find(&out).Error
}

// userTags loads the tag ids of many users in one query (Host API
// User.tags).
func userTags(db *gorm.DB) func(context.Context, []string) (map[string][]string, error) {
	return func(ctx context.Context, ids []string) (map[string][]string, error) {
		out := map[string][]string{}
		if len(ids) == 0 {
			return out, nil
		}
		var rows []struct{ UserID, TagID string }
		if err := db.WithContext(ctx).Table("user_tags").Select("user_id, tag_id").
			Where("user_id IN ?", ids).Order("user_id, tag_id").Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.UserID] = append(out[r.UserID], r.TagID)
		}
		return out, nil
	}
}

// configLookup is the configs store as the Host API reads it.
type configLookup interface {
	Lookup(owner, key string) (entity.Config, bool)
}

// ownedConfigs reads one config row with its real secret flag, so
// config.read_other sees plain values and only config.read_secret (with
// the row declared) reaches a secret.
type ownedConfigs struct{ c configLookup }

func (c ownedConfigs) Config(_ context.Context, owner, key string) (string, bool, bool, error) {
	row, ok := c.c.Lookup(owner, key)
	if !ok {
		return "", false, false, nil
	}
	return row.Value, true, row.IsSecret, nil
}

// connectedAccounts lists the accounts linked to a connector instance: names
// and owners only, never their tokens.
type connectedAccounts struct{ svc *connectors.Service }

func (a connectedAccounts) ConnectedAccounts(ctx context.Context, instanceID string) ([]*hostpb.AccountRef, error) {
	rows, err := a.svc.ListAccounts(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	out := make([]*hostpb.AccountRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, &hostpb.AccountRef{Id: r.ID, DisplayName: r.DisplayName, WickUserId: r.WickUserID})
	}
	return out, nil
}
