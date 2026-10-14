package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

type echoIn struct {
	Name string `wick:"desc=name"`
	N    int    `wick:"desc=n"`
}

func runOpTestModule() connector.Module {
	return connector.Module{
		Meta: connector.Meta{Key: "echo_test", Name: "Echo"},
		Operations: []connector.Category{connector.Cat("Echo", "echo",
			connector.Op("echo", "Echo", "echo input + config", echoIn{}, func(c *connector.Ctx) (any, error) {
				secret := c.Cfg("token")
				return map[string]any{
					"name":  c.Input("name"),
					"n":     c.InputInt("n"),
					"token": c.Mask(secret, []string{secret}),
					"note":  "uses " + secret,
				}, nil
			}, wickdocs.Docs{}),
			connector.Op("boom", "Boom", "fails", echoIn{}, func(c *connector.Ctx) (any, error) {
				return nil, errors.New("bad token " + c.Mask(c.Cfg("token"), []string{c.Cfg("token")}))
			}, wickdocs.Docs{}),
		)},
	}
}

func TestRunOpMainConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(cfg, []byte(`{"token":"s3cr\"et"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := RunOpMain(runOpTestModule(), []string{"--run-op", "echo", "--params", `{"name":"yoga","n":7}`, "--config", cfg, "--raw"}, &out, &errb)
	if code != runOpOK {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output not JSON: %s", out.String())
	}
	if got["name"] != "yoga" || got["n"] != float64(7) {
		t.Fatalf("input not passed: %v", got)
	}
	if got["token"] != "[masked]" || got["note"] != "uses [masked]" {
		t.Fatalf("secret not masked: %v", got)
	}
	if !strings.Contains(errb.String(), "2 masked value") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestRunOpMainErrorsAndUsage(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "cfg.json")
	_ = os.WriteFile(cfg, []byte(`{"token":"abc123"}`), 0o600)
	var out, errb bytes.Buffer
	if code := RunOpMain(runOpTestModule(), []string{"--run-op", "boom", "--config", cfg}, &out, &errb); code != runOpErr {
		t.Fatalf("boom code=%d", code)
	}
	if strings.Contains(errb.String(), "abc123") || !strings.Contains(errb.String(), "[masked]") {
		t.Fatalf("error leaked secret: %q", errb.String())
	}
	errb.Reset()
	if code := RunOpMain(runOpTestModule(), []string{"--run-op", "echo"}, &out, &errb); code != runOpUsage {
		t.Fatalf("missing config source code=%d", code)
	}
	if code := RunOpMain(runOpTestModule(), []string{"--run-op", "nope", "--config", cfg}, &out, &errb); code != runOpErr {
		t.Fatalf("unknown op code=%d", code)
	}
}

func TestHasRunOpFlag(t *testing.T) {
	if !hasRunOpFlag([]string{"--run-op", "x"}) || !hasRunOpFlag([]string{"--run-op=x"}) || hasRunOpFlag([]string{"--dump-manifest"}) {
		t.Fatal("hasRunOpFlag wrong")
	}
}
