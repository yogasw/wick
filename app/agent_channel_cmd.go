package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

/* ── the CLI channel ──────────────────────────────────────────────────────

   `support-tools agent send` is how WORK talks back to the agent that
   started it. A build script, a deploy, a migration: it finishes (or
   fails), it says so, and the session wakes on that message.

   The credential comes from the session itself — an agent mints it with
   the wick_cli_token MCP tool and puts it in the job's environment. This
   command only spends it. It cannot mint, and there is no --session flag
   to point it somewhere else: the token names the session, so a leaked one
   is useless against anybody else's conversation.
*/

const (
	envToken = "WICK_CLI_TOKEN"
	envBase  = "WICK_BASE_URL"
)

// Exit codes, so a script can branch on WHY it failed rather than on a
// message: a build that cannot report is different from one that reported
// to a dead session.
const (
	exitUsage    = 2 // nothing was sent: bad flags, no token
	exitAuth     = 3 // token expired, revoked, or for a session that is gone
	exitUnreach  = 4 // wick is not answering on this host
	exitRejected = 5 // wick answered, and said no
)

func agentChannelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Talk to an agent session from a script",
		Long: "Send a message into the wick session that minted your token.\n\n" +
			"The token comes from the session itself (the wick_cli_token MCP tool) and is\n" +
			"read from " + envToken + "; the host from " + envBase + " or --base-url.\n" +
			"Typical use, at the end of a build script:\n\n" +
			"  support-tools agent send --text \"0.1.255 built ok\"\n" +
			"  support-tools agent send --text \"build FAILED at step 3\" || true\n",
	}
	cmd.AddCommand(agentSendCmd(), agentWhoamiCmd())
	return cmd
}

func agentSendCmd() *cobra.Command {
	var text, file, token, base string
	c := &cobra.Command{
		Use:   "send",
		Short: "Send a message into the session that minted your token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			body, err := messageBody(text, file)
			if err != nil {
				return exitErr(exitUsage, err)
			}
			resp, err := callCLIAPI(http.MethodPost, base, token, "/api/cli/send",
				map[string]string{"text": body})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sent to session %s\n", resp["session_id"])
			return nil
		},
	}
	c.Flags().StringVar(&text, "text", "", "message to send")
	c.Flags().StringVar(&file, "file", "", "read the message from a file, or - for stdin")
	c.Flags().StringVar(&token, "token", "", "CLI token (default: $"+envToken+")")
	c.Flags().StringVar(&base, "base-url", "", "wick base URL (default: $"+envBase+", else http://127.0.0.1:9424)")
	return c
}

func agentWhoamiCmd() *cobra.Command {
	var token, base string
	c := &cobra.Command{
		Use:   "whoami",
		Short: "Show which session a token speaks into, and how long it has left",
		Long: "Check a token before relying on it. A build script should run this first:\n" +
			"it distinguishes 'my token expired' from 'wick is down' before the work starts,\n" +
			"which is the difference between a clear message and a silent failure at the end.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			resp, err := callCLIAPI(http.MethodGet, base, token, "/api/cli/whoami", nil)
			if err != nil {
				return err
			}
			out, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			if missing, _ := resp["session_missing"].(bool); missing {
				return exitErr(exitAuth, fmt.Errorf("the session this token belongs to no longer exists"))
			}
			return nil
		},
	}
	c.Flags().StringVar(&token, "token", "", "CLI token (default: $"+envToken+")")
	c.Flags().StringVar(&base, "base-url", "", "wick base URL (default: $"+envBase+")")
	return c
}

// messageBody resolves --text / --file / stdin into the message.
func messageBody(text, file string) (string, error) {
	switch {
	case text != "" && file != "":
		return "", fmt.Errorf("use --text or --file, not both")
	case file == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		text = string(b)
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", file, err)
		}
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("nothing to send: pass --text, --file, or --file - for stdin")
	}
	// A build log is not a message. Truncating here, loudly, beats wick
	// refusing a 4MB body after the build already finished.
	const maxRunes = 8000
	if r := []rune(text); len(r) > maxRunes {
		text = string(r[:maxRunes]) + "\n\n[truncated: the message was longer than 8000 characters]"
	}
	return text, nil
}

// callCLIAPI performs one request against the CLI channel and turns every
// failure into an error carrying the right exit code.
func callCLIAPI(method, base, token, path string, payload any) (map[string]any, error) {
	if token == "" {
		token = strings.TrimSpace(os.Getenv(envToken))
	}
	if token == "" {
		return nil, exitErr(exitUsage, fmt.Errorf(
			"no token: set %s, or pass --token. An agent mints one with the wick_cli_token MCP tool", envToken))
	}
	if base == "" {
		base = strings.TrimSpace(os.Getenv(envBase))
	}
	if base == "" {
		base = "http://127.0.0.1:9424"
	}
	base = strings.TrimRight(base, "/")

	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, exitErr(exitUsage, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		return nil, exitErr(exitUsage, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, exitErr(exitUnreach, fmt.Errorf("cannot reach wick at %s: %w", base, err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	msg, _ := out["error"].(string)
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, exitErr(exitAuth, fmt.Errorf("%s", msg))
	case resp.StatusCode == http.StatusNotFound:
		return nil, exitErr(exitAuth, fmt.Errorf("%s", msg))
	case resp.StatusCode >= 400:
		return nil, exitErr(exitRejected, fmt.Errorf("wick said no (%d): %s", resp.StatusCode, msg))
	}
	return out, nil
}

// exitErr wraps an error with the process exit code it should produce.
func exitErr(code int, err error) error {
	return cliExit{code: code, err: err}
}

type cliExit struct {
	code int
	err  error
}

func (e cliExit) Error() string { return e.err.Error() }
func (e cliExit) ExitCode() int { return e.code }
