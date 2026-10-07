package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
	"github.com/yogasw/wick/pkg/tool"
)

// apiProviderLoginTTYLogout is POST .../logintty/logout?login_provider=X[&account=aN]:
// removes every stored credential of one provider from the instance's
// store (omp `auth-broker logout`). A manage grant, like reconnect.
func apiProviderLoginTTYLogout(c *tool.Ctx) {
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
	prov := strings.TrimSpace(c.Query("login_provider"))
	// opencode: ?account=<folder> removes the login from that account
	// folder, not from the instance's main one.
	if acct := strings.TrimSpace(c.Query("account")); acct != "" && ins.Type == provider.TypeOpencode {
		derived, err := provider.WithOpencodeAccount(ins, acct)
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ins = derived
	}
	if err := logintty.LogoutProvider(ins.Type, provider.AccountEnv(ins), prov); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// The account lost a provider: its cached model list is wrong now.
	provider.InvalidateCLIModels(ins)
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

// apiProviderAPIKeySet is POST .../logintty/apikey with JSON body
// {provider, key} (a body, never the query, so the key stays out of access
// logs): stores the key as the instance Env var the CLI reads
// for that provider (empty key removes it), then refreshes the live model
// list so newly unlocked models show up. Editing Env is an admin write,
// same as the detail form's env editor.
func apiProviderAPIKeySet(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) || !requireProviderAdmin(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok || refuseSharer(c, ins) {
		return
	}
	var body struct {
		Provider string `json:"provider"`
		Key      string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(c.W, c.R.Body, 16<<10)).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	envVar, ok := logintty.APIKeyEnvVar(ins, strings.TrimSpace(body.Provider))
	if !ok {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "unknown API-key provider"})
		return
	}
	key := strings.TrimSpace(body.Key)
	if strings.ContainsAny(key, "\r\n") {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "key must be a single line"})
		return
	}
	ins.Env = logintty.SetEnvVar(ins.Env, envVar, key)
	if err := provider.Save(ins); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if provider.LiveModelsEnabled(ins) {
		saved := ins
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			// The key changed what the account may list: drop the cached
			// list and re-read it from the CLI's files / running server.
			provider.InvalidateCLIModels(saved)
			if _, _, err := provider.CachedCLIModels(ctx, saved, false); err != nil {
				log.Debug().Err(err).Str("provider", string(saved.Type)+"/"+saved.Name).Msg("live models refresh after API key save")
			}
		}()
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "env": envVar, "set": key != ""})
}
