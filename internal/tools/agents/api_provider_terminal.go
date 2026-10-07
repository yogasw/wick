package agents

import (
	"errors"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/managedbin"
	"github.com/yogasw/wick/internal/agents/terminal"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// api_provider_terminal.go is the Detail page's "Terminal" section for
// omp/opencode: a gotty web terminal running one allowlisted command
// under the instance's env, reached only through wick's reverse proxy.
// A terminal is a shell on the host, so every route is provider-admin
// only (the same guard as provider configuration) and the guard runs
// before anything else. gotty itself binds 127.0.0.1 with a random port
// and credential; see internal/agents/terminal.

// terminalGotty is the gotty binary to run: the wick-managed current
// version only (Download it in the section's Binary panel). A var so
// tests can point it at a fake.
var terminalGotty = func() (string, bool) {
	p, _, ok := managedbin.Default.CurrentPath(terminal.GottyType)
	return p, ok
}

// terminalManager is the manager the routes use (a var for tests).
var terminalManager = terminal.Default

// TerminalStatusResponse is GET …/terminal.
type TerminalStatusResponse struct {
	Commands       []terminal.Command `json:"commands"`
	GottyInstalled bool               `json:"gotty_installed"`
}

// TerminalStartResponse is POST …/terminal.
type TerminalStartResponse struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Command string `json:"command"`
}

// findTerminalInstance resolves {type}/{name} to an instance that has a
// web terminal at all.
func findTerminalInstance(c *tool.Ctx) (provider.Instance, bool) {
	ins, ok := findLoginInstance(c)
	if !ok {
		return ins, false
	}
	if len(terminal.Commands(ins)) == 0 {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no web terminal for provider type " + string(ins.Type)})
		return ins, false
	}
	return ins, true
}

// apiProviderTerminalStatus: GET /api/providers/{type}/{name}/terminal —
// the command allowlist and whether gotty is installed.
func apiProviderTerminalStatus(c *tool.Ctx) {
	if !requireProviderAdmin(c) || notReady(c) {
		return
	}
	ins, ok := findTerminalInstance(c)
	if !ok {
		return
	}
	_, installed := terminalGotty()
	c.JSON(http.StatusOK, TerminalStatusResponse{Commands: terminal.Commands(ins), GottyInstalled: installed})
}

// apiProviderTerminalStart: POST …/terminal?command=<key> — start gotty
// and return the proxied URL for the modal's iframe.
func apiProviderTerminalStart(c *tool.Ctx) {
	if !requireProviderAdmin(c) || notReady(c) {
		return
	}
	ins, ok := findTerminalInstance(c)
	if !ok {
		return
	}
	cmd, ok := terminal.LookupCommand(ins, strings.TrimSpace(c.Query("command")))
	if !ok {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "command not in the allowlist"})
		return
	}
	gotty, ok := terminalGotty()
	if !ok {
		c.JSON(http.StatusConflict, map[string]string{"error": "gotty is not installed — download it in the Terminal section first"})
		return
	}
	bin, found := provider.ResolveBinary(ins)
	if !found {
		c.JSON(http.StatusConflict, map[string]string{"error": "binary not found: " + bin})
		return
	}
	if ins.Type == provider.TypeOpencode {
		// The data dir must exist before opencode writes into it.
		if _, err := provider.OpencodeEnv(ins); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	who := ""
	if u := login.GetUser(c.Context()); u != nil {
		who = u.Email
	}
	s, err := terminalManager.Start(terminal.StartRequest{
		Instance: ins,
		Command:  cmd,
		GottyBin: gotty,
		CmdBin:   bin,
		// This route's own path: <tool base>/api/providers/{t}/{n}/terminal.
		BasePath: strings.TrimSuffix(c.R.URL.Path, "/") + "/",
		User:     who,
		Env:      terminal.Env(ins),
		Dir:      terminal.HomeDir(),
	})
	if errors.Is(err, terminal.ErrTooMany) {
		c.JSON(http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, TerminalStartResponse{ID: s.ID, URL: s.BasePath, Command: cmd.Label})
}

// terminalSession is the open session named by {id}, only when it
// belongs to the {type}/{name} in the path.
func terminalSession(c *tool.Ctx) (*terminal.Session, bool) {
	s := terminalManager.Get(c.PathValue("id"))
	if s == nil || string(s.Type) != c.PathValue("type") || s.Name != c.PathValue("name") {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no such terminal"})
		return nil, false
	}
	return s, true
}

// apiProviderTerminalClose: POST …/terminal/{id}/close — the modal was
// closed; kill gotty and its command now.
func apiProviderTerminalClose(c *tool.Ctx) {
	if !requireProviderAdmin(c) {
		return
	}
	s, ok := terminalSession(c)
	if !ok {
		return
	}
	s.Close("closed by user")
	c.JSON(http.StatusOK, map[string]bool{"closed": true})
}

// apiProviderTerminalProxy: GET …/terminal/{id}/{path...} — the gotty
// page, its assets and its websocket, proxied.
func apiProviderTerminalProxy(c *tool.Ctx) {
	if !requireProviderAdmin(c) {
		return
	}
	s, ok := terminalSession(c)
	if !ok {
		return
	}
	s.ServeHTTP(c.W, c.R)
}
