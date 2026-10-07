package agents

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
	"github.com/yogasw/wick/pkg/tool"
)

// loginTTY owns the per-instance reconnect (login) TTY sessions for
// the providers SPA.
var loginTTY = logintty.NewManager()

// LoginTTYSessionDTO is the running-session view for the SPA.
type LoginTTYSessionDTO struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	RemainingS int    `json:"remaining_s"`
	CapS       int    `json:"cap_s"`
}

// LoginTTYStatusResponse is GET /api/providers/{type}/{name}/logintty:
// whether reconnect is available for this type, who is currently
// connected per the credential files, and the live session if any.
type LoginTTYStatusResponse struct {
	Supported   bool                `json:"supported"`
	Account     logintty.Account    `json:"account"`
	Session     *LoginTTYSessionDTO `json:"session,omitempty"`
	DefaultTTLS int                 `json:"default_ttl_s"`
	ExtendS     int                 `json:"extend_s"`
	MaxTTLS     int                 `json:"max_ttl_s"`
	// LoginChoices is the picker shown before Login for types whose login
	// needs a provider choice (omp, opencode). Empty = no picker.
	LoginChoices []logintty.LoginChoice `json:"login_choices,omitempty"`
	// LoginNote is a caveat shown beside the picker (opencode: Claude
	// subscriptions unsupported).
	LoginNote string `json:"login_note,omitempty"`
	// AccountStore names where this instance's single account lives (omp
	// profile / opencode data dir), so the card can show it.
	AccountStore string `json:"account_store,omitempty"`
	// Accounts is the instance's credential pool (omp: several accounts
	// per profile, rotated on usage limits). Empty for other types.
	Accounts []logintty.PoolAccount `json:"accounts,omitempty"`
	// APIKeys are the API-key login choices (omp/opencode), each marked
	// set when the instance Env already carries its var.
	APIKeys []logintty.APIKeyProvider `json:"api_keys,omitempty"`
	// AuthFrom is the instance whose login this one uses (omp/opencode
	// shared login); account and usage above are that owner's, and the
	// card hides login / logout / add-account.
	AuthFrom string `json:"auth_from,omitempty"`
}

func loginTTYSessionDTO(s *logintty.Session) *LoginTTYSessionDTO {
	if s == nil {
		return nil
	}
	fr := s.TTLFrame()
	return &LoginTTYSessionDTO{
		ID:         s.ID,
		State:      s.State(),
		RemainingS: fr.RemainingS,
		CapS:       fr.CapS,
	}
}

// findLoginInstance resolves the {type}/{name} path pair to an
// instance, writing the 404 itself on miss.
// authOwnerName is the owner of ins's shared login, "" when it has its own.
func authOwnerName(ins provider.Instance) string {
	if o, ok := provider.AuthOwner(ins); ok {
		return o.Name
	}
	return ""
}

// refuseSharer answers 409 for a login write on an instance that uses
// another's login: it is done on the owner.
func refuseSharer(c *tool.Ctx, ins provider.Instance) bool {
	owner := authOwnerName(ins)
	if owner == "" {
		return false
	}
	c.JSON(http.StatusConflict, map[string]string{"error": fmt.Sprintf("%s uses the login of %s — log in or out there", ins.Name, owner)})
	return true
}

func findLoginInstance(c *tool.Ctx) (provider.Instance, bool) {
	t := provider.Type(c.PathValue("type"))
	name := c.PathValue("name")
	ins, err := provider.Find(t, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
		return provider.Instance{}, false
	}
	return ins, true
}

// apiProviderLoginTTYStatus reports connect status for one instance:
// account from the credential files (honouring the instance's env
// overrides like CLAUDE_CONFIG_DIR) plus the live login session.
func apiProviderLoginTTYStatus(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	if !requireProviderManage(c, ins.Type, ins.Name) {
		return
	}
	_, supported := logintty.LoginCommand(ins.Type, nil)
	c.JSON(http.StatusOK, LoginTTYStatusResponse{
		Supported:    supported,
		Account:      logintty.ReadAccount(ins.Type, provider.AccountEnv(ins)),
		Session:      loginTTYSessionDTO(loginTTY.Get(ins.Type, ins.Name)),
		DefaultTTLS:  int(logintty.DefaultTTL.Seconds()),
		ExtendS:      int(logintty.ExtendStep.Seconds()),
		MaxTTLS:      int(logintty.MaxTTL.Seconds()),
		LoginChoices: logintty.LoginChoices(ins),
		LoginNote:    logintty.LoginNote(ins.Type),
		AccountStore: accountStoreLabel(ins),
		Accounts:     statusAccounts(ins),
		APIKeys:      logintty.APIKeyProviders(ins),
		AuthFrom:     authOwnerName(ins),
	})
}

// statusAccounts is the instance's account list for the Connection card.
// omp rows carry their windows from the pool listing itself; opencode
// rows get theirs from the shared usage cache (never blocking, never a
// new request for an account already read).
func statusAccounts(ins provider.Instance) []logintty.PoolAccount {
	env := provider.AccountEnv(ins)
	accts := logintty.ListAccounts(ins.Type, env)
	if ins.Type != provider.TypeOpencode || len(accts) == 0 {
		return accts
	}
	v := usageProbes.get(logintty.UsageIdentity(ins.Type, env), func() ([]logintty.UsageWindow, error) {
		return logintty.ReadUsage(ins.Type, env)
	}, logintty.CredentialsChangedAt(ins.Type, env))
	if !v.Known || v.Err != nil {
		return accts
	}
	for i := range accts {
		accts[i].Usage, _ = logintty.AccountWindows(v.Windows, accts[i].ID)
	}
	return accts
}

// apiProviderLoginTTYUsage reports current rate-limit utilization for
// the connected account (claude's 5h/7d windows). Best-effort: types
// without a usage API return supported=false, a failed fetch returns
// the error string so the SPA can show "usage unavailable".
//
// Goes through the SAME per-account cache as the providers list, and
// that matters: this endpoint is one modal open away from being spammed,
// and its account is usually the one the list just probed. Opening a
// detail panel must reuse that reading, not buy a fresh 429.
func apiProviderLoginTTYUsage(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	if !requireProviderManage(c, ins.Type, ins.Name) {
		return
	}
	if !logintty.SupportsUsage(ins.Type) {
		c.JSON(http.StatusOK, map[string]any{"supported": false, "windows": []logintty.UsageWindow{}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), connectionsUsageTimeout)
	defer cancel()

	v := usageProbes.getWait(ctx, logintty.UsageIdentity(ins.Type, provider.AccountEnv(ins)), func() ([]logintty.UsageWindow, error) {
		return logintty.ReadUsage(ins.Type, provider.AccountEnv(ins))
	}, logintty.CredentialsChangedAt(ins.Type, provider.AccountEnv(ins)))
	body := map[string]any{"supported": true, "windows": []logintty.UsageWindow{}, "checking": v.Checking}
	// Same provenance the list carries: this panel is looking at a
	// SHARED, cached reading, so it says how old it is.
	now := time.Now()
	if !v.FetchedAt.IsZero() {
		body["fetched_at"] = v.FetchedAt.UTC().Format(time.RFC3339)
		body["age_s"] = int(v.Age(now).Round(time.Second) / time.Second)
	}
	if !v.NextAt.IsZero() {
		if d := v.NextAt.Sub(now); d > 0 {
			body["next_s"] = int(d.Round(time.Second) / time.Second)
		}
	}
	switch {
	case errors.Is(v.Err, logintty.ErrUsageUnsupported):
		c.JSON(http.StatusOK, map[string]any{"supported": false, "windows": []logintty.UsageWindow{}})
	case v.Err != nil:
		body["error"] = v.Err.Error()
		c.JSON(http.StatusOK, body)
	case !v.Known:
		// First reading for this account is still queued behind the
		// pacing gate: say "pending", never an error the user would
		// read as broken, and never a second request.
		body["pending"] = true
		c.JSON(http.StatusOK, body)
	default:
		if v.Windows != nil {
			body["windows"] = logintty.Headline(v.Windows)
		}
		c.JSON(http.StatusOK, body)
	}
}

// apiProviderLoginTTYUsageRefresh is the Retry / re-check button: it
// asks for a fresh reading of this account NOW, dropping the cache TTL
// and our own backoff.
//
// It is deliberately not a plain "fetch again" — that would hand the
// user a way to re-create the rate limit one click at a time. The cache
// still refuses inside a server-sent Retry-After, still refuses two
// probes within usageManualMinInterval, and still paces the outbound
// call. A refusal comes back as accepted=false with the wait, which the
// UI shows, rather than as a silent no-op.
//
// Returns immediately: the probe runs in the background and the next
// poll (or the panel's own refresh) picks the reading up.
func apiProviderLoginTTYUsageRefresh(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	if !requireProviderManage(c, ins.Type, ins.Name) {
		return
	}
	if !logintty.SupportsUsage(ins.Type) {
		c.JSON(http.StatusOK, map[string]any{"supported": false, "accepted": false})
		return
	}
	accepted, wait := usageProbes.forceRefresh(logintty.UsageIdentity(ins.Type, provider.AccountEnv(ins)), func() ([]logintty.UsageWindow, error) {
		return logintty.ReadUsage(ins.Type, provider.AccountEnv(ins))
	})
	body := map[string]any{"supported": true, "accepted": accepted, "checking": accepted}
	if !accepted {
		body["wait_s"] = int(wait.Round(time.Second) / time.Second)
	}
	c.JSON(http.StatusOK, body)
}

// apiProviderLoginTTYStart launches (or attaches to) the login TTY for
// one instance.
func apiProviderLoginTTYStart(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	if !requireProviderManage(c, ins.Type, ins.Name) || refuseSharer(c, ins) {
		return
	}
	bin, found := provider.ResolveBinary(ins)
	if !found {
		c.JSON(http.StatusConflict, map[string]string{"error": "binary not found: " + bin})
		return
	}
	// opencode keeps one credential per provider per data folder, so a
	// second account of a provider logs in to its own folder:
	// ?account=new creates <data dir>/accounts/aN, ?account=aN re-logs one.
	if acct := strings.TrimSpace(c.Query("account")); acct != "" && ins.Type == provider.TypeOpencode {
		var aerr error
		if acct == "new" {
			_, ins, aerr = provider.NewOpencodeAccount(ins)
		} else {
			ins, aerr = provider.WithOpencodeAccount(ins, acct)
		}
		if aerr != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": aerr.Error()})
			return
		}
	}
	// omp/opencode: which OAuth provider to log in to, picked in the UI.
	// Validated against an allowlist inside logintty; never raw argv.
	s, err := loginTTY.StartWith(ins, bin, strings.TrimSpace(c.Query("login_provider")))
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"session": loginTTYSessionDTO(s)})
}

// apiProviderLoginTTYExtend pushes the running session's kill deadline
// out one step.
func apiProviderLoginTTYExtend(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	if !requireProviderManage(c, ins.Type, ins.Name) {
		return
	}
	fr, ok := loginTTY.Extend(ins.Type, ins.Name)
	if !ok {
		c.JSON(http.StatusConflict, map[string]any{"error": "no running session or cap reached", "remaining_s": fr.RemainingS, "cap_s": fr.CapS})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"remaining_s": fr.RemainingS, "cap_s": fr.CapS})
}

// apiProviderLoginTTYKill terminates the running session.
func apiProviderLoginTTYKill(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	if !requireProviderManage(c, ins.Type, ins.Name) {
		return
	}
	if !loginTTY.Kill(ins.Type, ins.Name) {
		c.JSON(http.StatusConflict, map[string]string{"error": "no running session"})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "killed"})
}

var loginTTYUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Same-origin SPA; the agents auth middleware already gated this
	// request before the upgrade.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// loginTTYClientMsg is what the browser terminal sends: keystrokes
// (base64) or a resize.
type loginTTYClientMsg struct {
	T    string `json:"t"` // in | resize
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// apiProviderLoginTTYWS streams session frames to the browser terminal
// and feeds keystrokes back into the PTY.
func apiProviderLoginTTYWS(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	if !requireProviderManage(c, ins.Type, ins.Name) {
		return
	}
	s := loginTTY.Get(ins.Type, ins.Name)
	if s == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no login session"})
		return
	}

	restoreWSUpgradeHeader(c.R.Header)

	conn, err := loginTTYUpgrader.Upgrade(c.W, c.R, nil)
	if err != nil {
		log.Debug().
			Str("component", "logintty-ws").
			Str("type", string(ins.Type)).
			Str("name", ins.Name).
			Err(err).
			Msg("logintty-ws: upgrade failed")
		return
	}
	defer conn.Close()

	l := log.With().
		Str("component", "logintty-ws").
		Str("type", string(ins.Type)).
		Str("name", ins.Name).
		Str("session", s.ID).
		Logger()

	frames := make(chan logintty.Frame, 256)
	snapshot := s.Attach(frames)
	defer s.Detach(frames)

	writeFrame := func(fr logintty.Frame) error {
		b, err := json.Marshal(fr)
		if err != nil {
			return err
		}
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, b)
	}
	for _, fr := range snapshot {
		if err := writeFrame(fr); err != nil {
			return
		}
	}

	// Reader: keystrokes + resizes from the browser. Closes done on any
	// read error (tab closed).
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var in loginTTYClientMsg
			if json.Unmarshal(msg, &in) != nil {
				continue
			}
			switch in.T {
			case "in":
				data, err := base64.StdEncoding.DecodeString(in.Data)
				if err != nil || len(data) == 0 {
					continue
				}
				if err := s.Input(data); err != nil {
					l.Debug().Err(err).Msg("logintty-ws: input write failed")
				}
			case "resize":
				if err := s.Resize(in.Cols, in.Rows); err != nil {
					l.Debug().Err(err).Msg("logintty-ws: resize failed")
				}
			}
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-readerDone:
			return
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		case fr := <-frames:
			if err := writeFrame(fr); err != nil {
				return
			}
		}
	}
}

// restoreWSUpgradeHeader puts the "upgrade" token back into the
// Connection header of a websocket handshake that lost it on the way
// in. Some reverse proxies (nginx/ingress with a hardcoded
// `proxy_set_header Connection`) forward the client's
// "Upgrade: websocket" but rewrite Connection to "keep-alive";
// gorilla's Upgrade() then rejects the handshake with 400 and the SPA
// terminal never connects. The gotty proxy in internal/tty already
// does the same repair.
func restoreWSUpgradeHeader(h http.Header) {
	if strings.EqualFold(h.Get("Upgrade"), "websocket") &&
		!headerHasToken(h, "Connection", "upgrade") {
		h.Set("Connection", "Upgrade")
	}
}

// headerHasToken reports whether the comma-separated header named key
// contains token (case-insensitive), e.g. "Connection: keep-alive, Upgrade".
func headerHasToken(h http.Header, key, token string) bool {
	for _, v := range h[http.CanonicalHeaderKey(key)] {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// accountStoreLabel is the omp profile or opencode data dir of ins, "" for
// types whose account is not pinned by wick.
func accountStoreLabel(ins provider.Instance) string {
	switch ins.Type {
	case provider.TypeOMP:
		return "profile " + provider.OMPProfile(ins)
	case provider.TypeOpencode:
		if d, err := provider.OpencodeDataDir(ins); err == nil {
			return d
		}
	}
	return ""
}

// apiProviderCLIModels asks an omp/opencode instance's CLI for the models
// its logged-in account can use (`omp models --json` / `opencode models`),
// for the Model selection card (live list preview + Refresh). Read-only:
// the live filter/default are saved as ordinary config keys.
func apiProviderCLIModels(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	if !requireProviderManage(c, ins.Type, ins.Name) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), 60*time.Second)
	defer cancel()
	// Served from the per-instance cache (~10 min); ?refresh=1 re-execs.
	// Refresh does NOT forget refusals: `omp models` lists the provider's
	// catalog, not what this account may run, so a fresh list says nothing
	// about a model_not_found. A refusal goes when the model next works
	// (MarkModelWorked) or the operator re-checks it (…/cli-models/recheck).
	refresh := c.Query("refresh") == "1"
	seeds, fetchedAt, err := provider.CachedCLIModels(ctx, ins, refresh)
	if err != nil && len(seeds) == 0 {
		c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	type model struct {
		ID          string `json:"id"`
		Desc        string `json:"desc,omitempty"`
		Unavailable bool   `json:"unavailable,omitempty"`
		Reason      string `json:"reason,omitempty"`
		Default     bool   `json:"default,omitempty"`
	}
	toDTO := func(ms []provider.LiveModel) []model {
		out := make([]model, 0, len(ms))
		for _, s := range ms {
			out = append(out, model{ID: s.ID, Desc: s.Desc, Unavailable: s.Unavailable, Reason: s.Reason, Default: s.Default})
		}
		return out
	}
	// models = everything the CLI lists, refusals marked: the raw list the
	// FE previews an unsaved filter over. The EFFECTIVE list (saved filter,
	// chosen Default, refusals, the default a spawn runs) is not served
	// here: the page reads it from the composer picker's own endpoint,
	// GET /providers/options/{type}/{name}/models?all=1, as the wick
	// provider page does for its live sets. hosted_allowed tells the FE
	// whether opencode/… entries count.
	resp := map[string]any{
		"models":         toDTO(provider.MarkLiveModels(ins, seeds)),
		"hosted_allowed": ins.Type != provider.TypeOpencode || provider.OpencodeHostedAllowed(ins),
		"fetched_at":     fetchedAt.UTC().Format(time.RFC3339),
	}
	if fetchedAt.IsZero() {
		resp["fetched_at"] = "" // nothing known yet: the UI says "click Refresh"
	}
	if _, src := provider.CLIModelsInfo(ins); src != "" {
		resp["source"] = src
	}
	if err != nil {
		// Refresh failed; the last good list is still served.
		resp["error"] = err.Error()
	}
	c.JSON(http.StatusOK, resp)
}

// apiProviderCLIModelRecheck is the operator's explicit re-check of one
// refused model: the refusal is forgotten, so the next turn on it tries
// again (a second model_not_found records it again). Body {"model": id}.
func apiProviderCLIModelRecheck(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	if !requireProviderManage(c, ins.Type, ins.Name) {
		return
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := c.BindJSON(&body); err != nil || strings.TrimSpace(body.Model) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "model is required"})
		return
	}
	cleared := provider.ClearModelRefusal(ins, strings.TrimSpace(body.Model))
	c.JSON(http.StatusOK, map[string]any{"cleared": cleared})
}
