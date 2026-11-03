package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/codemode"
)

/* ── Code Mode from a shell ───────────────────────────────────────────────

   `support-tools codemode run` is wick_run_code for a script: the same
   runner, the same per-call checks, run as the session that minted the
   CLI token (see agent_channel_cmd.go). A cron job or a build step can run
   a connector script without an agent turn in between.
*/

// exitScript is a run that started and failed: the script threw, a call
// was refused, the timeout hit. Distinct from the transport codes so a
// caller can tell "my script is wrong" from "wick said no".
const exitScript = 1

// codemodeRunOpts is one `codemode run` invocation.
type codemodeRunOpts struct {
	file       string // local script, or - for stdin
	scriptPath string // relative to the session's project folder, read by wick
	lang       string
	input      string
	inputFile  string
	timeout    time.Duration
	asJSON     bool
	token      string
	base       string
}

func codemodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codemode",
		Short: "Run Code Mode scripts as the session that minted your token",
	}
	cmd.AddCommand(codemodeRunCmd())
	return cmd
}

func codemodeRunCmd() *cobra.Command {
	var o codemodeRunOpts
	c := &cobra.Command{
		Use:   "run [script.ts|script.js|-]",
		Short: "Run a Code Mode script (wick_run_code) from a shell",
		Long: "Run a JS/TS script against wick connectors, as the session that minted\n" +
			"$" + envToken + " (the wick_cli_token MCP tool). Every wick.call is checked as\n" +
			"that session's caller, exactly like wick_run_code.\n\n" +
			"The script is a local file (read here and sent), - for stdin, or --script-path\n" +
			"for a file wick reads from the session's project folder.\n\n" +
			"  support-tools codemode run report.ts --input '{\"days\":7}'\n" +
			"  support-tools codemode run --script-path codemode/report.ts --json\n\n" +
			"stdout gets the script's result as JSON (--json: the whole run); logs and a\n" +
			"one-line call summary go to stderr. Exit 0 ok, 1 the script failed,\n" +
			"2 usage, 3 token, 4 wick unreachable, 5 refused.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			if len(args) == 1 {
				o.file = args[0]
			}
			return runCodemode(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o)
		},
	}
	c.Flags().StringVar(&o.lang, "lang", "", "ts | js (default: from the file extension, else ts)")
	c.Flags().StringVar(&o.input, "input", "", "JSON passed to the script as input")
	c.Flags().StringVar(&o.inputFile, "input-file", "", "read the input JSON from a file")
	c.Flags().DurationVar(&o.timeout, "timeout", 0, "script timeout (default and max: wick's, 120s and 300s unless an admin changed them in the agents settings)")
	c.Flags().StringVar(&o.scriptPath, "script-path", "", "script relative to the session's project folder, read by wick")
	c.Flags().BoolVar(&o.asJSON, "json", false, "print the whole run (result, logs, calls) as JSON")
	c.Flags().StringVar(&o.token, "token", "", "CLI token (default: $"+envToken+")")
	c.Flags().StringVar(&o.base, "base-url", "", "wick base URL (default: $"+envBase+")")
	return c
}

// codemodePayload turns the flags into the /api/cli/codemode/run body.
func codemodePayload(stdin io.Reader, o codemodeRunOpts) (map[string]any, error) {
	if (o.file != "") == (strings.TrimSpace(o.scriptPath) != "") {
		return nil, fmt.Errorf("pass a script file (or - for stdin) or --script-path, exactly one")
	}
	p := map[string]any{}
	lang := strings.ToLower(strings.TrimSpace(o.lang))
	switch lang {
	case "", "ts", "js":
	default:
		return nil, fmt.Errorf("--lang must be ts or js, got %q", o.lang)
	}
	if o.scriptPath != "" {
		p["script_path"] = strings.TrimSpace(o.scriptPath)
		if lang == "" {
			lang = langOf(o.scriptPath)
		}
	} else {
		var code []byte
		var err error
		if o.file == "-" {
			code, err = io.ReadAll(stdin)
		} else {
			code, err = os.ReadFile(o.file)
			if lang == "" {
				lang = langOf(o.file)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("read script: %w", err)
		}
		if strings.TrimSpace(string(code)) == "" {
			return nil, fmt.Errorf("the script is empty")
		}
		p["code"] = string(code)
	}
	if lang != "" {
		p["lang"] = lang
	}

	input := o.input
	switch {
	case o.input != "" && o.inputFile != "":
		return nil, fmt.Errorf("use --input or --input-file, not both")
	case o.inputFile != "":
		b, err := os.ReadFile(o.inputFile)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", o.inputFile, err)
		}
		input = string(b)
	}
	if strings.TrimSpace(input) != "" {
		if !json.Valid([]byte(input)) {
			return nil, fmt.Errorf("the input is not valid JSON")
		}
		p["input"] = json.RawMessage(input)
	}
	if o.timeout < 0 {
		return nil, fmt.Errorf("--timeout must be positive")
	}
	if o.timeout > 0 {
		p["timeout_ms"] = o.timeout.Milliseconds()
	}
	return p, nil
}

// langOf reads the language off a file name; "" lets wick default it.
func langOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".js", ".mjs":
		return "js"
	case ".ts", ".mts":
		return "ts"
	}
	return ""
}

func runCodemode(stdin io.Reader, stdout, stderr io.Writer, o codemodeRunOpts) error {
	payload, err := codemodePayload(stdin, o)
	if err != nil {
		return exitErr(exitUsage, err)
	}
	// The run itself is bounded by wick, whose max an admin may raise in the
	// agents settings (never past codemode.HardMaxTimeout), so the client
	// waits for what it asked or the hard bound; the slack covers the first
	// run on a host, which downloads the runner.
	wait := o.timeout
	if wait <= 0 || wait > codemode.HardMaxTimeout {
		wait = codemode.HardMaxTimeout
	}
	raw, err := doCLIAPI(http.MethodPost, o.base, o.token, "/api/cli/codemode/run", payload, wait+6*time.Minute)
	if err != nil {
		return err
	}
	var res codemode.Result
	if err := json.Unmarshal(raw, &res); err != nil {
		return exitErr(exitRejected, fmt.Errorf("wick answered something that is not a run: %w", err))
	}

	if o.asJSON {
		var pretty bytes.Buffer
		if json.Indent(&pretty, raw, "", "  ") != nil {
			pretty.Reset()
			pretty.Write(raw)
		}
		fmt.Fprintln(stdout, pretty.String())
	} else {
		for _, l := range res.Logs {
			fmt.Fprintln(stderr, l)
		}
		if res.OK {
			out := []byte("null")
			if len(res.Result) > 0 {
				out = res.Result
			}
			var pretty bytes.Buffer
			if json.Indent(&pretty, out, "", "  ") != nil {
				pretty.Reset()
				pretty.Write(out)
			}
			fmt.Fprintln(stdout, pretty.String())
		}
		fmt.Fprintln(stderr, callSummary(res))
	}
	if !res.OK {
		return exitErr(exitScript, fmt.Errorf("script failed: %s", res.Error))
	}
	return nil
}

// callSummary is the one stderr line about the calls a run made, naming
// the ones that failed.
func callSummary(res codemode.Result) string {
	var failed []string
	for _, c := range res.Calls {
		if !c.OK {
			failed = append(failed, c.ToolID)
		}
	}
	noun := "calls"
	if len(res.Calls) == 1 {
		noun = "call"
	}
	s := fmt.Sprintf("codemode: %d %s", len(res.Calls), noun)
	if len(failed) > 0 {
		s += fmt.Sprintf(", %d failed (%s)", len(failed), strings.Join(failed, ", "))
	}
	if res.Truncated {
		s += ", result truncated"
	}
	return s + fmt.Sprintf(" · %dms", res.DurationMS)
}
