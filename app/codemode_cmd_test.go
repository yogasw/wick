package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var coded interface{ ExitCode() int }
	if !asCoded(err, &coded) {
		return -1
	}
	return coded.ExitCode()
}

// The flags become exactly one source, a language and the input; anything
// ambiguous is a usage error before a request is made.
func TestCodemodePayload(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "rows.js")
	if err := os.WriteFile(script, []byte("return 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	inFile := filepath.Join(dir, "in.json")
	if err := os.WriteFile(inFile, []byte(`{"days":7}`), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := codemodePayload(nil, codemodeRunOpts{file: script, input: `{"a":1}`, timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if p["code"] != "return 1" || p["lang"] != "js" || string(p["input"].(json.RawMessage)) != `{"a":1}` || p["timeout_ms"] != int64(5000) {
		t.Errorf("file payload = %v", p)
	}
	if _, has := p["script_path"]; has {
		t.Error("a local file must not send script_path")
	}

	p, err = codemodePayload(strings.NewReader("return 2"), codemodeRunOpts{file: "-", inputFile: inFile})
	if err != nil || p["code"] != "return 2" || string(p["input"].(json.RawMessage)) != `{"days":7}` {
		t.Errorf("stdin payload = %v, %v", p, err)
	}
	if _, has := p["lang"]; has {
		t.Error("stdin has no extension; the language is left to wick")
	}

	p, err = codemodePayload(nil, codemodeRunOpts{scriptPath: "codemode/report.ts"})
	if err != nil || p["script_path"] != "codemode/report.ts" || p["lang"] != "ts" || p["code"] != nil {
		t.Errorf("script_path payload = %v, %v", p, err)
	}

	for name, o := range map[string]codemodeRunOpts{
		"no source":     {},
		"both sources":  {file: script, scriptPath: "a.ts"},
		"bad lang":      {file: script, lang: "py"},
		"bad input":     {file: script, input: "{nope"},
		"two inputs":    {file: script, input: "{}", inputFile: inFile},
		"missing file":  {file: filepath.Join(dir, "gone.ts")},
		"empty stdin":   {file: "-"},
		"negative wait": {file: script, timeout: -time.Second},
	} {
		if _, err := codemodePayload(strings.NewReader(""), o); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// Exit codes and streams: the result alone on stdout, logs and the call
// summary on stderr, 1 for a script that failed, the channel's codes for
// everything that never ran.
func TestCodemodeRunOutputAndExitCodes(t *testing.T) {
	t.Setenv(envToken, "")
	t.Setenv(envBase, "")

	var status int
	var body string
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		gotBody = nil
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	run := func(o codemodeRunOpts) (string, string, int) {
		var out, errOut bytes.Buffer
		o.base, o.token = srv.URL, "wick_cli_x"
		err := runCodemode(strings.NewReader("return 1"), &out, &errOut, o)
		if err != nil {
			errOut.WriteString("error: " + err.Error())
		}
		return out.String(), errOut.String(), exitCodeOf(err)
	}

	status, body = http.StatusOK, `{"ok":true,"result":{"n":2},"logs":["hello"],"calls":[{"tool_id":"conn:a/x","ok":true,"ms":3},{"tool_id":"conn:b/y","ok":false,"ms":4,"error":"nope"}],"duration_ms":12,"run_id":"r1"}`
	out, errOut, code := run(codemodeRunOpts{file: "-"})
	if code != 0 || gotPath != "/api/cli/codemode/run" || gotAuth != "Bearer wick_cli_x" || gotBody["code"] != "return 1" {
		t.Fatalf("ok run: code %d path %q auth %q body %v", code, gotPath, gotAuth, gotBody)
	}
	if strings.TrimSpace(out) != "{\n  \"n\": 2\n}" {
		t.Errorf("stdout = %q, want the result alone", out)
	}
	if !strings.Contains(errOut, "hello") || !strings.Contains(errOut, "2 calls, 1 failed (conn:b/y)") {
		t.Errorf("stderr = %q", errOut)
	}

	out, _, code = run(codemodeRunOpts{file: "-", asJSON: true})
	var whole map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &whole) != nil || whole["run_id"] != "r1" {
		t.Errorf("--json: code %d out %q", code, out)
	}

	status, body = http.StatusOK, `{"ok":false,"error":"boom at line 3","logs":["before"],"calls":[],"duration_ms":5,"run_id":"r2"}`
	out, errOut, code = run(codemodeRunOpts{file: "-"})
	if code != exitScript || out != "" || !strings.Contains(errOut, "boom at line 3") || !strings.Contains(errOut, "before") {
		t.Errorf("failed script: code %d out %q err %q", code, out, errOut)
	}

	for _, c := range []struct {
		status int
		want   int
	}{
		{http.StatusUnauthorized, exitAuth},
		{http.StatusNotFound, exitAuth},
		{http.StatusBadRequest, exitRejected},
		{http.StatusServiceUnavailable, exitRejected},
	} {
		status, body = c.status, `{"error":"x"}`
		if _, _, code := run(codemodeRunOpts{file: "-"}); code != c.want {
			t.Errorf("HTTP %d: exit %d, want %d", c.status, code, c.want)
		}
	}

	if _, _, code := run(codemodeRunOpts{}); code != exitUsage {
		t.Errorf("no source: exit %d, want %d", code, exitUsage)
	}

	srv.Close()
	if _, _, code := run(codemodeRunOpts{file: "-"}); code != exitUnreach {
		t.Errorf("wick down: exit %d, want %d", code, exitUnreach)
	}
}
