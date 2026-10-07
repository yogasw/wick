package terminal

import (
	"strconv"
	"strings"

	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/pkg/envscrub"
)

// command.go is the allowlist of what a web terminal may run, and the
// argv/env gotty is started with. The browser only ever sends a command
// KEY; argv is built here from the instance, never from user input, and
// gotty runs without --permit-arguments so the URL cannot add any either.

// Command is one entry of the allowlist.
type Command struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Args is the argv after the provider binary.
	Args []string `json:"-"`
}

// Commands lists what a terminal may run for ins. nil = the type has no
// web terminal.
func Commands(ins provider.Instance) []Command {
	switch ins.Type {
	case provider.TypeOMP:
		p := provider.OMPProfile(ins)
		return []Command{
			{Key: "tui", Label: "omp --profile " + p, Args: []string{"--profile", p}},
			{Key: "login", Label: "omp login", Args: []string{"login"}},
			{Key: "usage", Label: "omp usage", Args: []string{"usage"}},
		}
	case provider.TypeOpencode:
		return []Command{
			{Key: "tui", Label: "opencode"},
			{Key: "auth-login", Label: "opencode auth login", Args: []string{"auth", "login"}},
			{Key: "auth-list", Label: "opencode auth list", Args: []string{"auth", "list"}},
		}
	}
	return nil
}

// LookupCommand finds key in ins's allowlist.
func LookupCommand(ins provider.Instance, key string) (Command, bool) {
	for _, c := range Commands(ins) {
		if c.Key == key {
			return c, true
		}
	}
	return Command{}, false
}

// Spec is everything one gotty start needs.
type Spec struct {
	Port       int
	ConfigPath string // 0600 gotty config carrying the credential (GottyConfig)
	BasePath   string // URL path gotty serves under, ends in "/"
	Bin        string // provider binary (absolute)
	Args       []string
}

// closeTimeoutS: after the browser disconnects gotty sends SIGHUP to the
// command, and SIGKILLs it this many seconds later.
const closeTimeoutS = 5

// firstClientTimeoutS: gotty exits when nobody connects within this.
const firstClientTimeoutS = 120

// GottyConfig is the private config file gotty reads the credential from.
// Never --credential: /proc/<pid>/cmdline is world-readable, and the
// credential is all that stands between another local user and a shell.
// Pointing --config at our own file also keeps a host ~/.gotty out.
func GottyConfig(credential string) []byte {
	return []byte("enable_basic_auth = true\ncredential = " + strconv.Quote(credential) + "\n")
}

// GottyArgs is gotty's argv. 127.0.0.1 only; --once exits after the one
// client; no --permit-arguments, no --random-url (the path is ours).
func GottyArgs(s Spec) []string {
	args := []string{
		"--config", s.ConfigPath,
		"--address", "127.0.0.1",
		"--port", strconv.Itoa(s.Port),
		"--path", s.BasePath,
		"--once",
		"--permit-write",
		"--timeout", strconv.Itoa(firstClientTimeoutS),
		"--close-timeout", strconv.Itoa(closeTimeoutS),
		"--title-format", "wick terminal",
		s.Bin,
	}
	return append(args, s.Args...)
}

// Env is the environment gotty (and so the command) runs with: the
// scrubbed host env, then the instance's account env exactly as a login
// or spawn gets it (OMP_PROFILE; opencode's XDG_* data/config dirs, which
// keep the host's ~/.config/opencode MCP servers out), with the opencode
// config overrides blanked the same way a spawn blanks them. Any GOTTY_*
// var is dropped: gotty reads every flag from env too.
func Env(ins provider.Instance) []string {
	env := append(envscrub.ScrubOSEnv(), provider.OwnAccountEnv(ins)...)
	if ins.Type == provider.TypeOpencode {
		env = append(env, "OPENCODE_CONFIG=", "OPENCODE_CONFIG_DIR=")
	}
	out := env[:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOTTY_") || strings.HasPrefix(kv, provider.AccountBinEnvKey+"=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
