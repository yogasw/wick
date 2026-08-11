// Command git serves the git CLI connector as a wick plugin.
//
// main has two modes. Normally it serves the connector over gRPC. When invoked
// with --askpass it acts as git's credential helper: git runs it, it prints the
// username or token from the environment, and exits. That second mode is why the
// credential never needs a file on disk — git requires an executable to call, and
// this binary already is one.
//
// --dump-manifest is deliberately NOT handled here. wickplugin.Serve owns it, so
// main must fall through to Serve for every argument other than --askpass.
package main

import (
	"fmt"
	"os"
	"strings"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// askpassFlag switches this binary into credential-helper mode explicitly. It
// exists for testing and manual verification.
//
// Real git does NOT use it. GIT_ASKPASS must be a bare executable path — git
// execs it directly and cannot carry an extra flag — so when git asks for a
// credential this process receives the prompt as argv[1] with no flag at all
// (see BuildEnv in git.go, which sets GIT_ASKPASS=<selfPath>). Both shapes are
// therefore accepted.
const askpassFlag = "--askpass"

// serveFlags are the arguments pkg/plugin's Serve owns. They must reach Serve
// untouched, and they are what separates a real git prompt from a plugin-host
// invocation: anything starting with "-" is a flag for Serve, never a prompt.
var serveFlags = map[string]bool{
	"--dump-manifest": true,
	"--sign-key":      true,
}

func main() {
	// git invokes GIT_ASKPASS with the prompt as argv[1]. Intercept before Serve,
	// which owns every other flag (--dump-manifest, --sign-key).
	if args := os.Args[1:]; isAskpassInvocation(args) {
		fmt.Println(askpassReply(askpassPrompt(args)))
		return
	}
	wickplugin.Serve(Module())
}

// isAskpassInvocation reports whether this process was started as git's askpass
// helper.
//
// Two shapes count, and only the leading argument is ever examined — matching
// anywhere in argv would let a prompt containing "--askpass" hijack the serve
// path:
//
//	<self> --askpass "<prompt>"   explicit, used by tests and manual checks
//	<self> "<prompt>"             what real git does via GIT_ASKPASS
//
// The second shape is deliberately narrow. It requires exactly one argument that
// does not begin with "-", so no flag Serve owns (now or later) can be mistaken
// for a prompt, and a multi-argument host invocation still reaches Serve.
func isAskpassInvocation(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == askpassFlag {
		return true
	}
	return len(args) == 1 && !strings.HasPrefix(args[0], "-") && !serveFlags[args[0]]
}

// askpassPrompt extracts the prompt git passed, for either invocation shape. git
// always supplies one; a bare --askpass yields "", which askpassReply answers
// with an empty string.
func askpassPrompt(args []string) string {
	if len(args) > 0 && args[0] == askpassFlag {
		if len(args) > 1 {
			return args[1]
		}
		return ""
	}
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// askpassReply answers a git credential prompt from the environment.
//
// This is a credential boundary: whatever this function prints is handed to the
// process that invoked us. git reuses GIT_ASKPASS for questions that are not
// credential prompts — host-key confirmations ("Are you sure you want to continue
// connecting?"), SSH key passphrases, and prompts from helper programs it shells
// out to. Answering any of those with the HTTPS token would hand the token to
// something that never asked for it, so anything not recognised as one of git's
// own HTTPS username/password prompts gets an empty reply.
//
// Matching is on the words git itself uses ("Username for ...", "Password for
// ...", "password:") and is case-insensitive because the capitalisation varies
// between git versions and transports. It deliberately does NOT match a bare
// "token", which appears in unrelated prompts such as a smartcard "PIN for
// token:".
func askpassReply(prompt string) string {
	p := strings.ToLower(prompt)
	switch {
	case strings.Contains(p, "username"):
		return os.Getenv(envAskpassUser)
	case strings.Contains(p, "password"), strings.Contains(p, "personal access token"):
		return os.Getenv(envAskpassToken)
	default:
		return ""
	}
}
