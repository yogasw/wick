package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// Credentials filled AFTER register for the token schemes (bearer /
// custom_header). Two modes, picked by the per-user toggle:
//
//   - per instance: custom header rows ticked "Config" are not part of
//     the server row — each instance carries its own value as an
//     instance config (filled on its Settings page, overridable per
//     session), so instances can hold different accounts and expose
//     different ops.
//   - per user (SSO): same model as the oauth per-user mode. The
//     instance holds no credential, each wick user saves their own
//     ConnectorAccount and every call runs with the caller's own values.
//     The account's AccessToken carries the typed values as one
//     encrypted JSON object (field key → value).

// userTokenBearerKey is the single field of a bearer per-user account.
const userTokenBearerKey = "token"

// UserTokenField is one value a user enters to connect their own
// account: the bearer token, or one custom header.
type UserTokenField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// perUserScheme reports whether an auth scheme supports per-user mode.
func perUserScheme(scheme string) bool {
	return scheme == "oauth" || isTokenScheme(scheme)
}

// isTokenScheme reports whether per-user accounts of the scheme are typed
// values (bearer token / custom headers) rather than an OAuth login.
func isTokenScheme(scheme string) bool {
	return scheme == "bearer" || scheme == "custom_header"
}

// UserTokenFields lists what each user enters for a per-user server.
// custom_header: the rows ticked "Config" are per-user, the others keep
// the admin's shared value (e.g. a public client ID). With no row ticked
// the secret rows are per-user, and with none of those every row is.
// nil for non-token schemes.
func UserTokenFields(srv *entity.CustomConnectorMCPServer) []UserTokenField {
	switch srv.AuthScheme {
	case "bearer":
		return []UserTokenField{{Key: userTokenBearerKey, Label: "Bearer token"}}
	case "custom_header":
		rows, err := ParseHeaderRows(srv.AuthHeaders)
		if err != nil {
			return nil
		}
		return userHeaderFields(rows)
	}
	return nil
}

func userHeaderFields(rows []HeaderRow) []UserTokenField {
	var marked, secret, all []UserTokenField
	for _, r := range rows {
		if r.Key == "" {
			continue
		}
		f := UserTokenField{Key: r.Key, Label: r.Key}
		all = append(all, f)
		if r.Config {
			marked = append(marked, f)
		}
		if r.Secret {
			secret = append(secret, f)
		}
	}
	switch {
	case len(marked) > 0:
		return marked
	case len(secret) > 0:
		return secret
	}
	return all
}

// splitAdminUserTokens takes the per-user values out of a token-scheme
// form: they never land on the server row (that would make them a shared
// credential again). Returns what the admin typed, so a tested form can
// keep it as the admin's own account.
func splitAdminUserTokens(f *ServerForm) map[string]string {
	vals := map[string]string{}
	switch f.AuthScheme {
	case "bearer":
		if v := strings.TrimSpace(f.AuthSecret); v != "" {
			vals[userTokenBearerKey] = v
		}
		f.AuthSecret = ""
	case "custom_header":
		perUser := map[string]bool{}
		for _, fl := range userHeaderFields(f.AuthHeaders) {
			perUser[fl.Key] = true
		}
		for i := range f.AuthHeaders {
			r := &f.AuthHeaders[i]
			if !perUser[r.Key] {
				continue
			}
			if v := strings.TrimSpace(r.Value); v != "" {
				vals[r.Key] = v
			}
			r.Value = ""
			r.Secret = true
			r.Config = true
		}
	}
	return vals
}

// SaveUserToken stores wickUserID's own values for a per-user token
// instance after checking them with a tools/list against the server — a
// wrong token is refused here, not on the user's first call. Re-saving
// replaces the user's previous account.
func (s *Service) SaveUserToken(ctx context.Context, instanceID, wickUserID, label string, values map[string]string) error {
	if wickUserID == "" {
		return fmt.Errorf("log in to connect an account")
	}
	inst, err := s.conns.Get(ctx, instanceID)
	if err != nil {
		return err
	}
	if !s.InstancePerUser(*inst) {
		return fmt.Errorf("this instance does not take per-user tokens")
	}
	srvRow, err := s.serverForKey(ctx, inst.Key)
	if err != nil {
		return err
	}
	if !isTokenScheme(srvRow.AuthScheme) {
		return fmt.Errorf("this connector uses %s auth, not a token", srvRow.AuthScheme)
	}
	clean := map[string]string{}
	for _, fl := range UserTokenFields(srvRow) {
		v := strings.TrimSpace(values[fl.Key])
		if v == "" {
			return fmt.Errorf("%s is required", fl.Label)
		}
		clean[fl.Key] = v
	}
	srv, err := resolveServerConfig(srvRow.URL, srvRow.AuthScheme, srvRow.AuthSecret, srvRow.AuthHeaders, srvRow.AuthExtra, srvRow.Headers)
	if err != nil {
		return err
	}
	applyUserTokenValues(&srv, clean)
	if res := s.mcp(nil).Probe(ctx, srv, nil); !res.OK {
		return fmt.Errorf("the server refused this token: %s", res.Error)
	}
	return s.storeUserToken(ctx, instanceID, wickUserID, label, clean)
}

// storeUserToken writes the values as wickUserID's account, encrypted.
// The account slot is (instance, user): the upsert replaces their old one.
func (s *Service) storeUserToken(ctx context.Context, instanceID, wickUserID, label string, values map[string]string) error {
	if label == "" {
		label = wickUserID
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	blob := string(raw)
	if s.keys != nil {
		if blob, err = s.keys.EncryptSecret(blob); err != nil {
			return fmt.Errorf("encrypt token: %w", err)
		}
	}
	// Keyed by the wick user, not the label: one token account per user.
	return s.saveAccountTokensAs(ctx, instanceID, wickUserID, "user:"+wickUserID, label, &oauthTokens{AccessToken: blob})
}

// applyUserToken puts a per-user token account's values on srv.
func (s *Service) applyUserToken(srv *serverConfig, acc *entity.ConnectorAccount) error {
	blob := acc.AccessToken
	if isSecretToken(blob) && s.keys != nil {
		dec, err := s.keys.DecryptSecret(blob)
		if err != nil {
			return fmt.Errorf("decrypt token of @%s: %w", acc.DisplayName, err)
		}
		blob = dec
	}
	vals := map[string]string{}
	if err := json.Unmarshal([]byte(blob), &vals); err != nil {
		return fmt.Errorf("account @%s holds no usable token — connect it again", acc.DisplayName)
	}
	applyUserTokenValues(srv, vals)
	return nil
}

// applyUserTokenValues overrides srv's credential with one user's values.
// Header rows stay flagged secret: the mcp client decrypts a value only
// when it is an encrypted token, so plain values pass through as-is.
func applyUserTokenValues(srv *serverConfig, vals map[string]string) {
	switch srv.AuthScheme {
	case "bearer":
		srv.AuthSecret = vals[userTokenBearerKey]
	case "custom_header":
		rows := make([]HeaderRow, len(srv.AuthHeaders))
		copy(rows, srv.AuthHeaders)
		for i := range rows {
			if v, ok := vals[rows[i].Key]; ok {
				rows[i].Value = v
				rows[i].Secret = true
			}
		}
		srv.AuthHeaders = rows
	}
}

// applyAccount authenticates srv as one per-user account, whatever the
// scheme: an OAuth access token (refreshed when due) or typed values.
func (s *Service) applyAccount(ctx context.Context, srvRow *entity.CustomConnectorMCPServer, srv *serverConfig, acc *entity.ConnectorAccount) error {
	if isTokenScheme(srvRow.AuthScheme) {
		return s.applyUserToken(srv, acc)
	}
	meta := parseOAuthMeta(srvRow.AuthExtra)
	tok, err := s.accountAccessToken(ctx, &meta, acc)
	if err != nil {
		return err
	}
	srv.AccessToken = tok
	return nil
}

// serverForKey resolves the MCP server row behind a custom connector key.
func (s *Service) serverForKey(ctx context.Context, key string) (*entity.CustomConnectorMCPServer, error) {
	defID, ok := s.DefIDForKey(key)
	if !ok {
		return nil, fmt.Errorf("connector %q is not a custom connector", key)
	}
	def, err := s.store.GetDef(ctx, defID)
	if err != nil {
		return nil, err
	}
	serverID := ServerIDForDef(def)
	if serverID == "" {
		return nil, fmt.Errorf("connector %q is not MCP-backed", key)
	}
	return s.store.GetServer(ctx, serverID)
}

// UsesUserToken reports whether key is a per-user token connector — its
// accounts are typed tokens, so the OAuth login route does not apply.
func (s *Service) UsesUserToken(ctx context.Context, key string) bool {
	srv, err := s.serverForKey(ctx, key)
	return err == nil && isTokenScheme(srv.AuthScheme) && s.OAuthPerUser(ctx, key)
}

// ── per-instance Config headers ──────────────────────────────────────

// headerCfgKey is the instance config key holding a Config header's value.
func headerCfgKey(header string) string {
	return "header_" + toFieldKey(header)
}

// configHeaderFields declares the instance configs of a custom_header
// server's "Config" rows — the module carries them so each instance's
// Settings page asks for its own value.
func configHeaderFields(srv *entity.CustomConnectorMCPServer) []DefField {
	if srv == nil || srv.AuthScheme != "custom_header" {
		return nil
	}
	rows, err := ParseHeaderRows(srv.AuthHeaders)
	if err != nil {
		return nil
	}
	var out []DefField
	for _, r := range rows {
		if r.Key == "" || !r.Config {
			continue
		}
		out = append(out, DefField{
			Key:      headerCfgKey(r.Key),
			Label:    r.Key,
			Widget:   "secret",
			Secret:   true,
			Required: true,
			Group:    "Auth headers",
			Desc:     "Sent as the " + r.Key + " header to the MCP server. Each instance holds its own value.",
		})
	}
	return out
}

// hasConfigHeaders reports whether a form has rows filled per instance.
func hasConfigHeaders(f *ServerForm) bool {
	if f.AuthScheme != "custom_header" {
		return false
	}
	for _, r := range f.AuthHeaders {
		if r.Key != "" && r.Config {
			return true
		}
	}
	return false
}

// splitAdminConfigHeaders takes the Config rows' values out of the form:
// they belong to an instance, never to the server row. Returns what the
// admin typed (instance config key → value) for the first instance.
func splitAdminConfigHeaders(f *ServerForm) map[string]string {
	vals := map[string]string{}
	for i := range f.AuthHeaders {
		r := &f.AuthHeaders[i]
		if r.Key == "" || !r.Config {
			continue
		}
		if v := strings.TrimSpace(r.Value); v != "" {
			vals[headerCfgKey(r.Key)] = v
		}
		r.Value = ""
		r.Secret = true
	}
	return vals
}

// applyConfigHeaders fills srv's Config rows from one instance's configs
// (get reads a config key). Returns the header names still empty.
func applyConfigHeaders(srv *serverConfig, get func(string) string) []string {
	var missing []string
	rows := make([]HeaderRow, len(srv.AuthHeaders))
	copy(rows, srv.AuthHeaders)
	for i := range rows {
		if !rows[i].Config {
			continue
		}
		v := get(headerCfgKey(rows[i].Key))
		if v == "" {
			missing = append(missing, rows[i].Key)
			continue
		}
		rows[i].Value = v
		rows[i].Secret = true
	}
	srv.AuthHeaders = rows
	return missing
}

// instanceConfigGetter reads an instance's stored configs (encrypted
// values stay encrypted — the mcp client decrypts secret header rows).
func (s *Service) instanceConfigGetter(instanceID string) func(string) string {
	return func(key string) string {
		if s.keys == nil {
			return ""
		}
		return s.keys.GetOwned("connector:"+instanceID, key)
	}
}

// borrowInstanceConfig fills srv's Config rows for a tools/list sync: the
// preferred instance first, then the first enabled instance with every
// value set. false = no instance filled in yet.
func (s *Service) borrowInstanceConfig(ctx context.Context, key, preferInstance string, srv *serverConfig) bool {
	rows, _ := s.conns.ListByKey(ctx, key)
	for _, r := range preferFirst(rows, preferInstance) {
		if r.Disabled {
			continue
		}
		try := *srv
		if len(applyConfigHeaders(&try, s.instanceConfigGetter(r.ID))) == 0 {
			*srv = try
			return true
		}
	}
	return false
}

// keepAdminConfig lands the Config values a tested form carried on a new
// first instance, so save means "connector + working instance".
func (s *Service) keepAdminConfig(ctx context.Context, def *entity.CustomConnector, srvRow *entity.CustomConnectorMCPServer, createdBy string, vals map[string]string) string {
	if len(vals) == 0 || s.keys == nil {
		return ""
	}
	inst, err := s.conns.Create(ctx, def.Key, def.Name, map[string]string{}, createdBy)
	if err != nil {
		log.Warn().Err(err).Msg("create first instance for config headers")
		return ""
	}
	s.ensureTagsForDef(ctx, def)
	owner := "connector:" + inst.ID
	if err := s.keys.EnsureOwned(ctx, owner, FieldsToConfigs(configHeaderFields(srvRow))...); err != nil {
		log.Warn().Err(err).Msg("register config header rows")
		return inst.ID
	}
	for k, v := range vals {
		if err := s.keys.SetOwned(ctx, owner, k, v); err != nil {
			log.Warn().Err(err).Str("key", k).Msg("store config header value")
		}
	}
	if _, err := s.registerAndSeedFor(ctx, def, inst.ID); err != nil {
		log.Warn().Err(err).Msg("sync ops after filling the first instance")
	}
	return inst.ID
}

// preferFirst orders instance rows with preferInstance (when present) first.
func preferFirst(rows []entity.Connector, preferInstance string) []entity.Connector {
	ordered := make([]entity.Connector, 0, len(rows))
	for _, r := range rows {
		if r.ID == preferInstance {
			ordered = append([]entity.Connector{r}, ordered...)
		} else {
			ordered = append(ordered, r)
		}
	}
	return ordered
}
