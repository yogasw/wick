package plugin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
	pb "github.com/yogasw/wick/pkg/plugin/proto"
)

// Run-op mode: run ONE operation of this connector in-process and print the
// result, without wick spawning the binary. Because the op runs in the same
// process, `go run` and `dlv debug` work as-is — no reattach, no wick-lab:
//
//	go run ./plugins/connector/metabase --run-op run_sql \
//	    --params '{"database_id":2,"sql":"select 1"}' --instance "Metabase Internal"
//	dlv debug ./plugins/connector/metabase -- --run-op run_sql --params-file p.json --config cfg.json
//
// Config comes from one of:
//
//   - --config <file.json>: a local JSON object of config keys (dummy or
//     staging credentials). Never talks to wick.
//   - --instance <id|label>: a real wick instance. There is no separate
//     "request" step: the first run files a plugin test request with wick
//     (WICK_BASE_URL plus a token) and exits with a message; it never waits.
//     Runs while the request is pending, or after it was rejected, are
//     refused the same way. Once an admin approves it on Admin → Plugins →
//     Development mode, the next run goes through. With WICK_PAT
//     (a person) the instance's config then comes to this process and the op
//     runs here, so breakpoints work. With WICK_CLI_TOKEN (an agent session
//     on the wick host) wick runs this binary itself and only the masked
//     result comes back. The approval is bound to this machine through a
//     secret kept in the user cache dir, and only ops not marked destructive
//     may run this way.
//
// Values the op passes to c.Mask come back as "[masked]" in the printed
// result — the local process has no wick encryption key.

// Run-op exit codes.
const (
	runOpOK    = 0
	runOpErr   = 1
	runOpUsage = 2
)

// hasRunOpFlag reports whether args ask for run-op mode.
func hasRunOpFlag(args []string) bool {
	for _, a := range args {
		if a == "--run-op" || strings.HasPrefix(a, "--run-op=") {
			return true
		}
	}
	return false
}

type runOpOptions struct {
	op         string
	params     string
	paramsFile string
	instance   string
	configFile string
	wickURL    string
	raw        bool
}

// RunOpMain runs run-op mode and returns the process exit code.
func RunOpMain(mod connector.Module, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(mod.Meta.Key+" --run-op", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o runOpOptions
	fs.StringVar(&o.op, "run-op", "", "operation key to run (required)")
	fs.StringVar(&o.params, "params", "", "op input as a JSON object")
	fs.StringVar(&o.paramsFile, "params-file", "", "file holding the op input JSON object")
	fs.StringVar(&o.instance, "instance", "", "wick connector instance id or label to borrow the config from (needs admin approval)")
	fs.StringVar(&o.configFile, "config", "", "local JSON file with the connector config (offline, no wick)")
	fs.StringVar(&o.wickURL, "wick-url", os.Getenv("WICK_BASE_URL"), "wick base URL for --instance (default $WICK_BASE_URL)")
	fs.BoolVar(&o.raw, "raw", false, "print the result JSON compact instead of indented")
	if err := fs.Parse(args); err != nil {
		return runOpUsage
	}
	if o.op == "" {
		fmt.Fprintln(stderr, "--run-op <operation> is required")
		return runOpUsage
	}
	if (o.instance == "") == (o.configFile == "") {
		fmt.Fprintln(stderr, "pass exactly one of --instance <id|label> or --config <file.json>")
		return runOpUsage
	}
	if o.params != "" && o.paramsFile != "" {
		fmt.Fprintln(stderr, "pass --params or --params-file, not both")
		return runOpUsage
	}

	// A borrowed production config is for reading only: ops marked
	// destructive run with --config (your own credentials) or not at all.
	if o.instance != "" {
		for _, op := range mod.AllOps() {
			if op.Key == o.op && op.Destructive {
				fmt.Fprintf(stderr, "%s is a destructive op; --instance only runs non-destructive ops (use --config with your own credentials)\n", o.op)
				return runOpUsage
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	input, rawInput, err := loadRunOpParams(o)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return runOpUsage
	}

	var cfg map[string]string
	instanceID := ""
	if o.configFile != "" {
		cfg, err = loadRunOpConfigFile(o.configFile)
	} else {
		var c *wickTestClient
		if c, err = newWickTestClient(o); err == nil {
			var v pluginTestView
			if v, err = requireApproval(ctx, c, mod.Meta.Key, o); err == nil {
				if c.agent {
					// Agent session: wick runs this binary; nothing to run here.
					out, rerr := runViaWick(ctx, c, v, o.op, rawInput)
					if rerr != nil {
						fmt.Fprintln(stderr, rerr)
						return runOpErr
					}
					printRunOpResult(stdout, out, o.raw)
					return runOpOK
				}
				cfg, instanceID, err = borrowInstanceConfig(ctx, c, v)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return runOpErr
	}

	out, nMasked, err := runOpLocal(ctx, mod, o.op, instanceID, cfg, input, rawInput)
	if nMasked > 0 {
		fmt.Fprintf(stderr, "run-op: %d masked value(s) shown as [masked]\n", nMasked)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return runOpErr
	}
	printRunOpResult(stdout, out, o.raw)
	return runOpOK
}

func printRunOpResult(stdout io.Writer, out []byte, raw bool) {
	if !raw {
		var buf bytes.Buffer
		if json.Indent(&buf, out, "", "  ") == nil {
			out = buf.Bytes()
		}
	}
	fmt.Fprintln(stdout, string(out))
}

// loadRunOpParams turns the op input JSON object into the string map the
// host would send (strings as-is, everything else as JSON) plus the raw form.
func loadRunOpParams(o runOpOptions) (map[string]string, []byte, error) {
	raw := []byte(o.params)
	if o.paramsFile != "" {
		b, err := os.ReadFile(o.paramsFile)
		if err != nil {
			return nil, nil, fmt.Errorf("read --params-file: %w", err)
		}
		raw = b
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]string{}, nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil, fmt.Errorf("op input must be a JSON object: %w", err)
	}
	return stringifyMap(m), raw, nil
}

func loadRunOpConfigFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read --config: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("--config must be a JSON object: %w", err)
	}
	return stringifyMap(m), nil
}

func stringifyMap(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case string:
			out[k] = t
		case nil:
			out[k] = ""
		default:
			b, _ := json.Marshal(t)
			out[k] = string(b)
		}
	}
	return out
}

// runOpLocal executes the op through the same code path the gRPC server
// uses, then blanks every value the op asked to mask.
func runOpLocal(ctx context.Context, mod connector.Module, op, instanceID string, cfg, input map[string]string, rawInput []byte) ([]byte, int, error) {
	srv := NewServer(mod).(*grpcServer)
	args, _ := json.Marshal(input)
	req := &pb.ExecuteRequest{
		Operation:    op,
		ArgsJson:     args,
		RawInputJson: rawInput,
		Creds:        cfg,
		InstanceId:   instanceID,
	}
	result, masks, opErr := srv.runOp(ctx, req, nil)
	if opErr != nil {
		return nil, 0, fmt.Errorf("%s: %s", opErr.Code, maskText(opErr.Message, masks))
	}
	out, n := maskResult(result, masks)
	return out, n, nil
}

const maskedPlaceholder = "[masked]"

// maskResult replaces each recorded mask value inside the result JSON. The
// value is matched in its JSON-escaped form, so it is found wherever it sits
// inside a string.
func maskResult(result []byte, m *maskRecorder) ([]byte, int) {
	s := string(result)
	n := 0
	replace := func(v string, fold bool) {
		esc, _ := json.Marshal(v)
		needle := strings.Trim(string(esc), `"`)
		if needle == "" {
			return
		}
		if fold {
			re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(needle))
			n += len(re.FindAllStringIndex(s, -1))
			s = re.ReplaceAllLiteralString(s, maskedPlaceholder)
			return
		}
		n += strings.Count(s, needle)
		s = strings.ReplaceAll(s, needle, maskedPlaceholder)
	}
	for _, v := range m.exact {
		replace(v, false)
	}
	for _, v := range m.folded {
		replace(v, true)
	}
	return []byte(s), n
}

func maskText(s string, m *maskRecorder) string {
	for _, v := range m.exact {
		if v != "" {
			s = strings.ReplaceAll(s, v, maskedPlaceholder)
		}
	}
	for _, v := range m.folded {
		if v != "" {
			s = regexp.MustCompile("(?i)"+regexp.QuoteMeta(v)).ReplaceAllLiteralString(s, maskedPlaceholder)
		}
	}
	return s
}

// ── borrowing a wick instance's config ────────────────────────────────

type pluginTestView struct {
	ID         string     `json:"id"`
	State      string     `json:"state"`
	InstanceID string     `json:"instance_id"`
	Instance   string     `json:"instance"`
	ExpiresAt  *time.Time `json:"expires_at"`
	ApproveURL string     `json:"approve_url"`
	// Created is true when this call filed a new request.
	Created bool `json:"created"`
}

// newWickTestClient builds the client for --instance from the environment.
// WICK_PAT = a person: the config comes to this process. WICK_CLI_TOKEN (and
// no PAT) = an agent session on the wick host: wick runs this binary itself
// and only the masked result comes back.
func newWickTestClient(o runOpOptions) (*wickTestClient, error) {
	base := strings.TrimRight(strings.TrimSpace(o.wickURL), "/")
	if base == "" {
		return nil, errors.New("--instance needs the wick URL: set WICK_BASE_URL or pass --wick-url")
	}
	c := &wickTestClient{base: base, http: &http.Client{Timeout: 5 * time.Minute}}
	if pat := strings.TrimSpace(os.Getenv("WICK_PAT")); pat != "" {
		c.token = pat
	} else if cli := strings.TrimSpace(os.Getenv("WICK_CLI_TOKEN")); cli != "" {
		c.token, c.agent = cli, true
	} else {
		return nil, errors.New("--instance needs a token: WICK_PAT (a person, create one at /profile/tokens) or WICK_CLI_TOKEN (an agent session on the wick host)")
	}
	secret, err := runOpClientSecret()
	if err != nil {
		return nil, err
	}
	c.secret = secret
	c.machine = runOpMachine()
	return c, nil
}

// runOpClientSecret returns this machine's run-op secret, creating it on
// first use. wick stores only its hash with each request and wants the
// secret back on every use, so an approval works only where it was asked
// for. Kept in the user cache dir (0600), never sent anywhere else.
func runOpClientSecret() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate cache dir for the run-op secret: %w", err)
	}
	dir = filepath.Join(dir, "wick-runop")
	path := filepath.Join(dir, "client-secret")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
		return strings.TrimSpace(string(b)), nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(buf)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		return "", fmt.Errorf("write run-op secret: %w", err)
	}
	return secret, nil
}

// requireApproval files (or finds) this runner's plugin test request and
// returns it only when it is approved. It never waits: a new, pending,
// rejected, revoked or expired request is an error the caller prints, and
// the person reruns once an admin has decided.
func requireApproval(ctx context.Context, c *wickTestClient, key string, o runOpOptions) (pluginTestView, error) {
	host, _ := os.Hostname()
	if wd, err := os.Getwd(); err == nil {
		host += ":" + wd
	}
	var v pluginTestView
	if err := c.do(ctx, http.MethodPost, "/api/plugin-tests", map[string]string{
		"connector_key": key, "instance": o.instance, "operation": o.op, "host": host,
		"client_secret": c.secret, "machine": c.machine,
	}, &v); err != nil {
		return v, err
	}
	switch {
	case v.State == "active":
		return v, nil
	case v.State == "pending" && v.Created:
		return v, fmt.Errorf("plugin test request %s filed (%s · %s, %s). Not run: ask an admin to approve it at %s, then run again", v.ID, key, v.Instance, c.machine, v.ApproveURL)
	case v.State == "pending":
		return v, fmt.Errorf("plugin test request %s is still waiting for approval at %s; refused until an admin approves it", v.ID, v.ApproveURL)
	default:
		return v, fmt.Errorf("plugin test request %s is %s; refused", v.ID, v.State)
	}
}

// borrowInstanceConfig fetches an approved request's config into this
// process (person / PAT path).
func borrowInstanceConfig(ctx context.Context, c *wickTestClient, v pluginTestView) (map[string]string, string, error) {
	var cfgResp struct {
		Configs map[string]string `json:"configs"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/plugin-tests/"+v.ID+"/config", nil, &cfgResp); err != nil {
		return nil, "", err
	}
	return cfgResp.Configs, v.InstanceID, nil
}

// runViaWick asks wick to run this very binary against the approved
// instance (agent path). The config stays inside wick; the result comes back
// already masked (wick_enc_ tokens).
func runViaWick(ctx context.Context, c *wickTestClient, v pluginTestView, op string, rawInput []byte) ([]byte, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate this binary: %w", err)
	}
	input := map[string]any{}
	if len(bytes.TrimSpace(rawInput)) > 0 {
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("op input must be a JSON object: %w", err)
		}
	}
	var out struct {
		RunID  string          `json:"run_id"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	err = c.do(ctx, http.MethodPost, "/api/plugin-tests/"+v.ID+"/run", map[string]any{
		"binary": self, "operation": op, "input": input,
	}, &out)
	if err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, errors.New(out.Error)
	}
	return out.Result, nil
}

// runOpMachine is "<os user>@<hostname>". wick binds an approval to it and
// refuses a use from anything else.
func runOpMachine() string {
	host, _ := os.Hostname()
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	return name + "@" + host
}

type wickTestClient struct {
	base, token, secret, machine string
	agent                        bool // token is a wick_cli_ session token
	http                         *http.Client
}

func (c *wickTestClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if c.secret != "" {
		req.Header.Set("X-Plugin-Test-Secret", c.secret)
	}
	if c.machine != "" {
		req.Header.Set("X-Plugin-Test-Machine", c.machine)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("wick %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("wick: %s", e.Error)
		}
		return fmt.Errorf("wick %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode wick response: %w", err)
		}
	}
	return nil
}
