package hostapi

import (
	"context"
	"net/http"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// PATConfigKey is the plugin config that holds the PAT user-level calls run
// as when no person triggered the plugin (a cron job, a webhook).
const PATConfigKey = "wick_pat"

// PluginInfo is what the Server needs to know about an installed plugin.
type PluginInfo struct {
	Key     string
	Kind    string
	Version string
	// ConfigOwner is the configs owner of the plugin's own rows (where
	// wick_pat lives); "" when the plugin has no config store.
	ConfigOwner string
	Scopes      []string
	Resources   *wickplugin.HostResources
}

// PluginDirectory resolves a plugin key to its manifest facts.
type PluginDirectory interface {
	Plugin(key string) (PluginInfo, bool)
}

// UserDirectory lists wick users (the admin users repo satisfies it).
type UserDirectory interface {
	ListUsers(ctx context.Context) ([]*entity.User, error)
}

// ConfigReader reads one config row by owner and key.
type ConfigReader interface {
	Config(ctx context.Context, owner, key string) (value string, found, secret bool, err error)
}

// TokenAuthenticator resolves a personal access token to its owner
// (accesstoken.Service satisfies it).
type TokenAuthenticator interface {
	Authenticate(ctx context.Context, plain string) (userID string, err error)
}

// AccountDirectory lists the accounts linked to a connector instance.
type AccountDirectory interface {
	ConnectedAccounts(ctx context.Context, instanceID string) ([]*hostpb.AccountRef, error)
}

// SessionService is the session metadata a plugin may see, always for
// userID (the acting person) and under that person's access rules.
type SessionService interface {
	ListSessions(ctx context.Context, userID string, in *hostpb.ListSessionsRequest) ([]*hostpb.SessionMeta, error)
	GetSessionMeta(ctx context.Context, userID, id string) (*hostpb.SessionMeta, error)
	SendToSession(ctx context.Context, userID, sessionID, text string) (queued bool, err error)
}

// TicketService is the ticket board as userID sees and edits it. Ticket ids
// are unique per project only: a request without project_id that matches in
// more than one project is FailedPrecondition, never a guess.
type TicketService interface {
	GetTicket(ctx context.Context, userID string, in *hostpb.GetTicketRequest) (*hostpb.Ticket, error)
	ListTickets(ctx context.Context, userID string, in *hostpb.ListTicketsRequest) ([]*hostpb.Ticket, error)
	CreateTicket(ctx context.Context, userID string, in *hostpb.CreateTicketRequest) (*hostpb.Ticket, error)
	UpdateTicket(ctx context.Context, userID string, in *hostpb.UpdateTicketRequest) (*hostpb.Ticket, error)
	AttachSession(ctx context.Context, userID string, in *hostpb.AttachSessionRequest) error
}

// Principals loads the acting user and their filter tags: the pair the PAT
// middleware stamps on a request (login.Service satisfies it).
type Principals interface {
	GetUserByID(ctx context.Context, id string) (*entity.User, error)
	GetUserFilterTagIDs(ctx context.Context, userID string) []string
}

// Principal is the acting person a user-level call runs as.
type Principal struct {
	User   *entity.User
	TagIDs []string
}

// ConnectorCall is one ExecuteConnector request after the gate.
type ConnectorCall struct {
	Connector  string
	InstanceID string
	Op         string
	Args       map[string]any
	AccountID  string
	// PluginKey names the plugin in the run history.
	PluginKey string
}

// ConnectorResult is the masked outcome of one connector op.
type ConnectorResult struct {
	RunID  string
	Status string
	// ResultJSON is the op's response after wick's masking.
	ResultJSON string
}

// ConnectorRunner lists and runs connector instances exactly as a person
// reaches them through wick_list / wick_execute (NewConnectorRunner wraps
// connectors.Service).
type ConnectorRunner interface {
	// ListInstances lists the enabled instances p can see; connector ""
	// means every module.
	ListInstances(ctx context.Context, p Principal, connector string) ([]*hostpb.ConnectorInstance, error)
	// Op reports whether connector has op and whether the op is destructive.
	Op(connector, op string) (destructive, found bool)
	// Execute runs one op as p. An instance p cannot see, or one of another
	// module, is NotFound.
	Execute(ctx context.Context, p Principal, call ConnectorCall) (*ConnectorResult, error)
}

// Deps wires the Server to wick. A nil dependency makes the RPCs that need
// it answer Unimplemented (the SDK reports ErrHostAPIUnavailable).
type Deps struct {
	Plugins PluginDirectory
	Users   UserDirectory
	// UserTags returns the tag ids of many users in one query, for
	// User.tags (optional).
	UserTags func(ctx context.Context, userIDs []string) (map[string][]string, error)
	Configs  ConfigReader
	PATs     TokenAuthenticator
	Accounts AccountDirectory
	Sessions SessionService
	Tickets  TicketService
	// Principals resolves the acting user for Rest and connectors.
	Principals Principals
	// Connectors backs ListConnectorInstances and ExecuteConnector.
	Connectors ConnectorRunner
	// Router is wick's own HTTP handler (the one the public listener
	// serves); Rest dispatches into it in-process.
	Router http.Handler
	// DB is wick's database handle; Query runs on it and never opens a
	// connection of its own.
	DB    *gorm.DB
	Perms *Permissions
	Audit *Audit
	// WickVersion is reported by Whoami.
	WickVersion string
}
