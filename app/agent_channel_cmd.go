package app

import (
	"bytes"
	"encoding/json"
	"errors"
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
	cmd.AddCommand(agentSendCmd(), agentTodoCmd(), agentWhoamiCmd())
	return cmd
}

// agentTodoCmd is progress without a wake-up.
//
// `send` is for things somebody has to react to; a run that says "3 of 9
// packages" every minute is not one of them — it would be nine turns of
// tokens for news nobody has to act on. This writes the session's checklist
// instead: the panel keeps it, a reload still shows it, and it costs nothing
// when nobody is looking.
func agentTodoCmd() *cobra.Command {
	var item, title, desc, status, unit, detail, detailFile, format, note, token, base string
	var done, total int
	var stop, clear, clearAll bool
	c := &cobra.Command{
		Use:   "todo",
		Short: "Update this session's checklist from a script (no wake-up)",
		Long: "Report progress into the session's checklist panel.\n\n" +
			"Only the item you name is touched, so a script does not have to know the\n" +
			"rest of the list — an item that does not exist yet is created.\n\n" +
			"  support-tools agent todo --item gate --status running --done 3 --total 9 \\\n" +
			"      --unit packages --detail-file gate.log --format text\n" +
			"  support-tools agent todo --item gate --status done\n" +
			"  support-tools agent todo --stop --note \"killed by the watchdog\"\n",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			payload := map[string]any{}
			switch {
			case clear || clearAll:
				payload["clear"] = true
				payload["clear_all"] = clearAll
			case stop:
				payload["stop"] = true
				payload["note"] = note
			default:
				if strings.TrimSpace(item) == "" {
					return exitErr(exitUsage, fmt.Errorf("--item is required (or use --stop / --clear)"))
				}
				payload["item"] = item
				payload["title"] = title
				payload["description"] = desc
				payload["status"] = status
				if cmd.Flags().Changed("done") {
					payload["done"] = done
				}
				if cmd.Flags().Changed("total") {
					payload["total"] = total
				}
				payload["unit"] = unit
				body, err := todoDetailBody(detail, detailFile)
				if err != nil {
					return exitErr(exitUsage, err)
				}
				if body != "" {
					payload["detail"] = body
					payload["format"] = format
				}
			}
			resp, err := callCLIAPI(http.MethodPost, base, token, "/api/cli/todo", payload)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "todo updated in session %s\n", resp["session_id"])
			return nil
		},
	}
	c.Flags().StringVar(&item, "item", "", "item id or title to update (created when new)")
	c.Flags().StringVar(&title, "title", "", "display title, when it differs from --item")
	c.Flags().StringVar(&desc, "description", "", "one line under the title")
	c.Flags().StringVar(&status, "status", "", "pending | running | done | failed | stopped")
	c.Flags().IntVar(&done, "done", 0, "units finished, for the progress bar")
	c.Flags().IntVar(&total, "total", 0, "units in total, for the progress bar")
	c.Flags().StringVar(&unit, "unit", "", "what the numbers count (packages, files, MB…)")
	c.Flags().StringVar(&detail, "detail", "", "payload to show under the item")
	c.Flags().StringVar(&detailFile, "detail-file", "", "read the payload from a file, or - for stdin")
	c.Flags().StringVar(&format, "format", "text", "text | markdown | json | html | xml")
	c.Flags().BoolVar(&stop, "stop", false, "mark the live list stopped (a run that died, a card that is stuck)")
	c.Flags().StringVar(&note, "note", "", "why it stopped")
	c.Flags().BoolVar(&clear, "clear", false, "delete the finished lists")
	c.Flags().BoolVar(&clearAll, "clear-all", false, "delete the finished lists AND the live one")
	c.Flags().StringVar(&token, "token", "", "CLI token (default: $"+envToken+")")
	c.Flags().StringVar(&base, "base-url", "", "wick base URL (default: $"+envBase+")")
	return c
}

// todoDetailBody resolves --detail / --detail-file / stdin, and keeps the
// TAIL of anything long: the end of a log is the part that says what
// happened, and the server clamps it again anyway.
func todoDetailBody(detail, file string) (string, error) {
	switch {
	case detail != "" && file != "":
		return "", fmt.Errorf("use --detail or --detail-file, not both")
	case file == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		detail = string(b)
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", file, err)
		}
		detail = string(b)
	}
	const maxRunes = 8000
	if r := []rune(detail); len(r) > maxRunes {
		detail = "[truncated: kept the last 8000 characters]\n" + string(r[len(r)-maxRunes:])
	}
	return detail, nil
}

func agentSendCmd() *cobra.Command {
	var text, file, token, base string
	var retry time.Duration
	c := &cobra.Command{
		Use:   "send",
		Short: "Send a message into the session that minted your token",
		Long: "Send a message into the wick session that minted your token.\n\n" +
			"Waits out a restart: wick may be handing over to a new binary at the exact\n" +
			"moment a build finishes, so an unreachable or still-booting daemon is retried\n" +
			"for --retry (default 90s) before giving up. An expired token or a refusal is\n" +
			"NOT retried — neither gets better by asking again.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			body, err := messageBody(text, file)
			if err != nil {
				return exitErr(exitUsage, err)
			}
			resp, err := sendWithRetry(cmd.ErrOrStderr(), base, token, body, retry)
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
	c.Flags().DurationVar(&retry, "retry", 90*time.Second, "keep retrying an unreachable or booting wick for this long (0 = fail fast)")
	return c
}

// sendWithRetry delivers the message, waiting out a daemon that is down or
// still booting.
//
// A build finishing at the same second wick swaps binaries is not a rare
// race: deploys are exactly when builds run. The port is normally kept open
// across a handover, but a full restart closes it for the successor's whole
// boot — around eighty seconds on a modest host — and a report lost to that
// window is the failure this whole channel exists to prevent.
//
// Only the transport failures are retried. An expired token and a refusal
// are answers, not outages, and repeating them just delays the exit code
// the script needs.
func sendWithRetry(errOut io.Writer, base, token, body string, budget time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(budget)
	wait := time.Second
	for attempt := 1; ; attempt++ {
		resp, err := callCLIAPI(http.MethodPost, base, token, "/api/cli/send",
			map[string]string{"text": body})
		if err == nil {
			return resp, nil
		}
		var coded cliExit
		if !errors.As(err, &coded) || coded.code != exitUnreach || budget <= 0 || time.Now().After(deadline) {
			return nil, err
		}
		left := time.Until(deadline).Round(time.Second)
		fmt.Fprintf(errOut, "wick not answering (attempt %d) — retrying in %s, giving up in %s\n",
			attempt, wait, left)
		if left <= 0 {
			return nil, err
		}
		if wait > left {
			wait = left
		}
		time.Sleep(wait)
		if wait < 8*time.Second {
			wait *= 2
		}
	}
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
