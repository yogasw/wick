package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
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
//   - --instance <id|label>: a real wick instance. The binary files a plugin
//     test request with wick (WICK_BASE_URL + a personal access token in
//     WICK_PAT), waits until an admin approves it on /admin/plugins/dev-mode,
//     then receives that instance's config. wick must have plugin tests
//     switched on and this connector in its allowlist, and only ops not
//     marked destructive may run this way.
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
	wait       time.Duration
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
	fs.DurationVar(&o.wait, "wait", 15*time.Minute, "how long to wait for admin approval")
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
		cfg, instanceID, err = borrowInstanceConfig(ctx, mod.Meta.Key, o, stderr)
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
	if !o.raw {
		var buf bytes.Buffer
		if json.Indent(&buf, out, "", "  ") == nil {
			out = buf.Bytes()
		}
	}
	fmt.Fprintln(stdout, string(out))
	return runOpOK
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
}

// borrowInstanceConfig files (or reuses) a plugin test request, waits for
// approval, and fetches the instance config.
func borrowInstanceConfig(ctx context.Context, key string, o runOpOptions, stderr io.Writer) (map[string]string, string, error) {
	base := strings.TrimRight(strings.TrimSpace(o.wickURL), "/")
	if base == "" {
		return nil, "", errors.New("--instance needs the wick URL: set WICK_BASE_URL or pass --wick-url")
	}
	pat := strings.TrimSpace(os.Getenv("WICK_PAT"))
	if pat == "" {
		return nil, "", errors.New("--instance needs a wick personal access token in WICK_PAT (create one at /profile/tokens)")
	}
	c := &wickTestClient{base: base, pat: pat, http: &http.Client{Timeout: 30 * time.Second}}

	host, _ := os.Hostname()
	if wd, err := os.Getwd(); err == nil {
		host += ":" + wd
	}
	var v pluginTestView
	if err := c.do(ctx, http.MethodPost, "/api/plugin-tests", map[string]string{
		"connector_key": key, "instance": o.instance, "operation": o.op, "host": host,
	}, &v); err != nil {
		return nil, "", err
	}

	deadline := time.Now().Add(o.wait)
	announced := false
	for v.State == "pending" {
		if !announced {
			fmt.Fprintf(stderr, "run-op: waiting for an admin to approve plugin test %s (%s · %s) at %s\n", v.ID, key, v.Instance, v.ApproveURL)
			announced = true
		}
		if time.Now().After(deadline) {
			return nil, "", fmt.Errorf("not approved within %s (request %s stays pending; rerun to keep waiting)", o.wait, v.ID)
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
		if err := c.do(ctx, http.MethodGet, "/api/plugin-tests/"+v.ID, nil, &v); err != nil {
			return nil, "", err
		}
	}
	if v.State != "active" {
		return nil, "", fmt.Errorf("plugin test %s is %s", v.ID, v.State)
	}

	var cfgResp struct {
		Configs   map[string]string `json:"configs"`
		ExpiresAt *time.Time        `json:"expires_at"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/plugin-tests/"+v.ID+"/config", nil, &cfgResp); err != nil {
		return nil, "", err
	}
	if cfgResp.ExpiresAt != nil {
		fmt.Fprintf(stderr, "run-op: using %s · %s (approval valid until %s)\n", key, v.Instance, cfgResp.ExpiresAt.Local().Format(time.Kitchen))
	}
	return cfgResp.Configs, v.InstanceID, nil
}

type wickTestClient struct {
	base, pat string
	http      *http.Client
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
	req.Header.Set("Authorization", "Bearer "+c.pat)
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
