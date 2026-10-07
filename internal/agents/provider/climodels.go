package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/internal/pkg/envscrub"
)

// climodels.go asks an omp / opencode instance which models its logged-in
// account can use, for the model picker's "refresh" action. Seeds in
// models.json stay the offline default; this is the live list.
//
//   - omp: `omp --profile <p> models --json` → {"models":[{"selector":
//     "provider/id","name":…}]} (coding-agent/src/cli/models-cli.ts
//     toModelJson, commands/models.ts --json).
//   - opencode: the instance server's GET /provider (connected providers'
//     models, opencode's default first — opencode_catalog.go); when that
//     is unavailable, `opencode models` → one "provider/model" per line
//     (packages/opencode/src/cli/cmd/models.ts); no JSON flag exists.

// cliModelsRunner execs one CLI; swapped in tests. One at a time across
// all instances (AcquireHelperSlot), inside the memory guard like an agent
// spawn ("omp-models" / "opencode-models" scope), with the limit of the
// instance env belongs to (HelperCommand).
var cliModelsRunner = func(ctx context.Context, bin string, args, env []string) ([]byte, error) {
	free, err := AcquireHelperSlot(ctx)
	defer free()
	if err != nil {
		return nil, err
	}
	label := strings.TrimSuffix(filepath.Base(bin), ".exe") + "-models"
	cmd, release := HelperCommand(ctx, InstanceForAccountEnv(env), label, bin, args...)
	defer release()
	cmd.Env = append(envscrub.ScrubOSEnv(), env...)
	return cmd.Output()
}

// ListCLIModels returns the live model list for an omp/opencode instance.
func ListCLIModels(ctx context.Context, ins Instance) ([]ModelSeed, error) {
	var args []string
	switch ins.Type {
	case TypeOMP:
		args = append(OMPProfileArgs(ins), "models", "--json")
	case TypeOpencode:
		if _, err := OpencodeEnv(ins); err != nil {
			return nil, err
		}
		if models := catalogModels(ctx, ins); len(models) > 0 {
			return models, nil
		}
		args = []string{"models"}
	default:
		return nil, fmt.Errorf("model refresh is not available for %s", ins.Type)
	}
	bin, found := ResolveBinary(ins)
	if !found {
		return nil, fmt.Errorf("%s binary not found: %s", ins.Type, bin)
	}
	out, err := cliModelsRunner(ctx, bin, args, AccountEnv(ins))
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("%s models: %s", ins.Type, firstLine(strings.TrimSpace(string(ee.Stderr))))
		}
		return nil, fmt.Errorf("%s models: %w", ins.Type, err)
	}
	if ins.Type == TypeOMP {
		return parseOMPModels(out)
	}
	return parseOpencodeModels(out), nil
}

// catalogModels is the opencode model list from the instance's server
// catalog; nil when there is none, so the CLI answers instead.
func catalogModels(ctx context.Context, ins Instance) []ModelSeed {
	cat, err := FetchOpencodeCatalog(ctx, ins)
	if err != nil {
		return nil
	}
	return cat.Models()
}

func parseOMPModels(out []byte) ([]ModelSeed, error) {
	s := bytes.TrimSpace(out)
	if i := bytes.IndexByte(s, '{'); i > 0 {
		s = s[i:]
	}
	var doc struct {
		Models []struct {
			Selector string `json:"selector"`
			Name     string `json:"name"`
			Kind     string `json:"kind"`
		} `json:"models"`
	}
	if err := json.Unmarshal(s, &doc); err != nil {
		return nil, fmt.Errorf("omp models --json: %w", err)
	}
	seeds := make([]ModelSeed, 0, len(doc.Models))
	for _, m := range doc.Models {
		if m.Selector == "" || (m.Kind != "" && m.Kind != "chat") {
			continue
		}
		seeds = append(seeds, ModelSeed{ID: m.Selector, Desc: m.Name})
	}
	return seeds, nil
}

func parseOpencodeModels(out []byte) []ModelSeed {
	var seeds []ModelSeed
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// Only "provider/model" lines; anything else is banner/log noise.
		if line == "" || strings.ContainsAny(line, " \t{}") || !strings.Contains(line, "/") {
			continue
		}
		seeds = append(seeds, ModelSeed{ID: line})
	}
	return seeds
}
