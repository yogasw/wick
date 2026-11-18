package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// accessRequestHint ends a refusal of an agent's op so the agent knows
// there is a way forward that does not need the owner to widen its
// access for good.
const accessRequestHint = "(to run it anyway, ask a person with wick_request_access: they approve this exact action and it runs with their access)"

// AccessQuery is one refused op the access check is asked about.
type AccessQuery struct {
	SessionID    string
	AgentID      string
	ConnectorID  string
	ConnectorKey string
	OpKey        string
	Destructive  bool
	Input        map[string]string
	// Config holds the instance's non-secret subject settings (its
	// app_code and the like), so a rule can tell which app a connector
	// is bound to when the op itself names none.
	Config map[string]string
}

// subjectConfigKeys are the instance config keys a rule may read. Only
// names of things, never credentials.
var subjectConfigKeys = []string{
	"app_code", "app_id", "appCode", "appId", "appid", "app",
	"env", "environment", "base_url", "workspace", "project", "repo", "repository",
}

func subjectConfig(all map[string]string) map[string]string {
	out := map[string]string{}
	for _, k := range subjectConfigKeys {
		if v, ok := all[k]; ok && v != "" && !strings.HasPrefix(v, "wick_enc_") {
			out[k] = v
		}
	}
	return out
}

// AccessCheck answers a refused op: the user it may run as instead
// (runAs, with a note for the log), nothing ("" and nil: the refusal
// stands), or an error that refuses it outright (a deny rule).
type AccessCheck func(ctx context.Context, q AccessQuery) (runAs, note string, err error)

// SetAccessCheck wires the access-request grants and rules. Set once at
// boot.
func (s *Service) SetAccessCheck(f AccessCheck) { s.accessCheck = f }

type runAsIdentity struct {
	user *entity.User
	tags []string
}

// accessOverride asks the access check about an op the agent scope
// refused, and resolves who it may run as. The person must still reach
// the connector on their own: a grant never outlives its approver's
// access.
func (s *Service) accessOverride(ctx context.Context, p ExecuteParams, c *entity.Connector, op *connector.Operation) (*runAsIdentity, error) {
	// Off for this session's project: nothing is asked, the refusal stands
	// exactly as before access requests existed.
	if s.accessCheck == nil || s.identity == nil || noAccessCheck(ctx) || !s.accessOn(p.SessionID) {
		return nil, nil
	}
	virtual := p.SessionInstance != nil
	var cfg map[string]string
	if virtual {
		cfg = subjectConfig(p.SessionInstance.Config)
	} else {
		cfg = subjectConfig(s.LoadConfigs(*c))
	}
	input := p.Input
	if len(p.ConfigOverride) > 0 {
		// The override is part of what is approved: a rule sees it and an
		// approval made without it does not cover it.
		input = make(map[string]string, len(p.Input)+1)
		for k, v := range p.Input {
			input[k] = v
		}
		b, _ := json.Marshal(p.ConfigOverride)
		input[ConfigOverrideParam] = string(b)
	}
	who, note, err := s.accessCheck(ctx, AccessQuery{
		SessionID: p.SessionID, AgentID: agentIDOf(ctx), ConnectorID: c.ID, ConnectorKey: c.Key,
		OpKey: op.Key, Destructive: op.Destructive, Input: input, Config: cfg,
	})
	if err != nil {
		return nil, fmt.Errorf("operation %q: %w", op.Key, err)
	}
	if who == "" {
		return nil, nil
	}
	u, tags, err := s.identity(ctx, who)
	if err != nil || u == nil {
		return nil, nil
	}
	if virtual {
		log.Info().Str("connector", c.Key).Str("op", op.Key).Str("approved_by", u.ID).Str("by", note).
			Str("session", p.SessionID).Msg("agent op on a session connector allowed by access approval")
		return &runAsIdentity{user: u, tags: tags}, nil
	}
	if vis, err := s.isVisibleDirect(ctx, c.ID, u.ID, tags, u.IsAdmin()); err != nil || !vis {
		return nil, nil
	}
	log.Info().Str("connector", c.ID).Str("op", op.Key).Str("run_as", u.ID).Str("by", note).
		Str("session", p.SessionID).Msg("agent op allowed by access request approval")
	return &runAsIdentity{user: u, tags: tags}, nil
}

// OpInfo describes one operation of a connector instance.
type OpInfo struct {
	ConnectorKey   string
	ConnectorLabel string
	OpName         string
	Destructive    bool
}

// DescribeOp resolves a connector instance and one of its operations.
func (s *Service) DescribeOp(ctx context.Context, connectorID, opKey string) (OpInfo, error) {
	return s.DescribeOpWith(ctx, connectorID, opKey, nil)
}

// DescribeOpWith is DescribeOp for a call carrying a config override: the
// override must be one the instance allows, and an op it switches on is
// no longer refused as off.
func (s *Service) DescribeOpWith(ctx context.Context, connectorID, opKey string, override map[string]string) (OpInfo, error) {
	c, err := s.repo.Get(ctx, connectorID)
	if err != nil {
		return OpInfo{}, fmt.Errorf("connector %q not found", connectorID)
	}
	if c.Disabled {
		return OpInfo{}, fmt.Errorf("connector %q is disabled", c.Label)
	}
	mod, ok := s.Module(c.Key)
	if !ok {
		return OpInfo{}, fmt.Errorf("connector %q has no implementation loaded", c.Label)
	}
	for _, op := range mod.AllOps() {
		if op.Key == opKey {
			if op.ConfigOnly {
				return OpInfo{}, fmt.Errorf("operation %q is a config helper, not an action", opKey)
			}
			if err := s.checkOverride(mod, *c, override); err != nil {
				return OpInfo{}, err
			}
			// An op the instance's own config switches off cannot run for
			// anyone, so asking for it would only fail after an approval —
			// unless the override switches it on.
			if states, err := s.OperationStatesFull(ctx, connectorID, c.Key); err == nil {
				if st, ok := states[opKey]; ok && st.HiddenByConfig && !overrideOpens(mod, s.LoadConfigs(*c), override, op) {
					return OpInfo{}, fmt.Errorf("operation %q is %s", opKey, s.configOffReasonFor(*c, op, true))
				}
			}
			return OpInfo{ConnectorKey: c.Key, ConnectorLabel: c.Label, OpName: op.Name, Destructive: op.Destructive}, nil
		}
	}
	return OpInfo{}, fmt.Errorf("unknown operation %q on connector %q", opKey, c.Label)
}

// CanRunOwn reports whether userID can run opKey on connectorID with their
// own access: they reach the instance, the op is switched on, and an
// admin-only op needs an admin. why says what is missing, in words.
func (s *Service) CanRunOwn(ctx context.Context, userID, connectorID, opKey string) (bool, string) {
	return s.CanRunOwnWith(ctx, userID, connectorID, opKey, nil)
}

// CanRunOwnWith is CanRunOwn for a call carrying a config override: the
// user must also be allowed to configure the instance.
func (s *Service) CanRunOwnWith(ctx context.Context, userID, connectorID, opKey string, override map[string]string) (bool, string) {
	if s.identity == nil || userID == "" {
		return false, "unknown user"
	}
	u, tags, err := s.identity(ctx, userID)
	if err != nil || u == nil {
		return false, "unknown user"
	}
	ctx = WithoutAgentScope(ctx)
	vis, err := s.isVisibleDirect(ctx, connectorID, u.ID, tags, u.IsAdmin())
	if err != nil || !vis {
		return false, "this connector is not in your access"
	}
	c, err := s.repo.Get(ctx, connectorID)
	if err != nil {
		return false, "connector not found"
	}
	mod, hasMod := s.Module(c.Key)
	if len(override) > 0 {
		if !hasMod {
			return false, "connector has no implementation loaded"
		}
		if err := s.checkOverride(mod, *c, override); err != nil {
			return false, err.Error()
		}
		if !s.CanConfigure(ctx, u.ID, c.ID) {
			return false, "running it with a " + ConfigOverrideParam + " needs someone who may configure this connector"
		}
	}
	states, err := s.OperationStatesFull(ctx, connectorID, c.Key)
	if err == nil {
		if st, ok := states[opKey]; ok {
			if !st.Enabled {
				return false, "this operation is switched off on the connector"
			}
			if st.HiddenByConfig {
				if hasMod {
					for _, op := range mod.AllOps() {
						if op.Key == opKey {
							if overrideOpens(mod, s.LoadConfigs(*c), override, op) {
								break
							}
							return false, "this operation is " + s.configOffReasonFor(*c, op, true)
						}
					}
				} else {
					return false, "this operation is turned off by this connector's config"
				}
			}
			if st.AdminOnly && !u.IsAdmin() {
				return false, "this operation is restricted to admins"
			}
		}
	}
	return true, ""
}

// RunOwn executes an action as userID with their own access (no agent
// scope, no lend). The run is filed under the user, with the source MCP.
func (s *Service) RunOwn(ctx context.Context, userID, connectorID, opKey, accountID string, params map[string]any) (string, string, error) {
	if s.identity == nil {
		return "", "", errors.New("identity lookup not wired")
	}
	u, tags, err := s.identity(ctx, userID)
	if err != nil || u == nil {
		return "", "", fmt.Errorf("approver %q not found", userID)
	}
	ctx = WithoutAgentScope(ctx)
	override, params, err := OverrideFromParams(params)
	if err != nil {
		return "", "", err
	}
	input := make(map[string]string, len(params))
	for k, v := range params {
		input[k] = stringifyParam(v)
	}
	res, err := s.Execute(ctx, ExecuteParams{
		ConnectorID: connectorID, OperationKey: opKey, Input: input, RawInput: params,
		Source: entity.ConnectorRunSourceMCP, UserID: u.ID, IsAdmin: u.IsAdmin(), TagIDs: tags, AccountID: accountID,
		ConfigOverride: override,
	})
	if err != nil {
		body := err.Error()
		runID := ""
		if res != nil {
			runID = res.RunID
			if res.ErrorMessage != "" {
				body = res.ErrorMessage
			}
		}
		return runID, "", errors.New(body)
	}
	return res.RunID, res.ResponseJSON, nil
}

// stringifyParam renders one raw argument the way the MCP layer does for
// Input: strings as is, whole numbers without a decimal point, the rest
// as JSON.
func stringifyParam(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

type noAccessCheckKey struct{}

// WithoutAccessCheck marks ctx so a refused op is never sent to the access
// check: a rule script's own connector calls must not ask the rules again.
func WithoutAccessCheck(ctx context.Context) context.Context {
	return context.WithValue(ctx, noAccessCheckKey{}, true)
}

func noAccessCheck(ctx context.Context) bool {
	v, _ := ctx.Value(noAccessCheckKey{}).(bool)
	return v
}

// ReadOnlyScope is an AgentScope that reaches every connector its holder
// can see but runs only non-destructive operations.
type ReadOnlyScope struct{}

func (ReadOnlyScope) AllowConnector(string) bool                        { return true }
func (ReadOnlyScope) AllowAccount(string, string) bool                  { return true }
func (ReadOnlyScope) AllowOp(_ string, _ string, destructive bool) bool { return !destructive }

// SubjectConfig is a connector instance's non-secret subject settings
// (app_code, base_url, …), nil when it does not exist.
func (s *Service) SubjectConfig(ctx context.Context, connectorID string) map[string]string {
	c, err := s.Get(ctx, connectorID)
	if err != nil || c == nil {
		return nil
	}
	return subjectConfig(s.LoadConfigs(*c))
}
