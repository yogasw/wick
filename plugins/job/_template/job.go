package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
)

// Config is the job's settings, editable from the Jobs page. The field NAME
// becomes the config key in snake_case (TargetURL → target_url); entries in one
// `wick:"..."` tag are `;`-separated (desc=, secret, required).
type Config struct {
	TargetURL string `wick:"required;url;desc=URL to GET on every run. Example: https://example.com/health"`
}

// Run is one execution. Read config with job.FromContext(ctx).Cfg, emit progress
// lines with job.Logf (they land in the run history), and return a markdown
// result. Return an error to mark the run failed. Honour ctx: the host cancels
// it when the run times out.
func Run(ctx context.Context) (string, error) {
	url := job.FromContext(ctx).Cfg("target_url")
	if url == "" {
		return "", fmt.Errorf("target_url is not set")
	}
	job.Logf(ctx, "GET %s", url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	job.Logf(ctx, "status %d", resp.StatusCode)
	return fmt.Sprintf("**%d** from `%s`\n\n```\n%s\n```", resp.StatusCode, url, strings.TrimSpace(string(body))), nil
}

// Module returns the job definition.
func Module() job.Module {
	return job.Module{
		Meta: job.Meta{
			// Key MUST equal your folder name (lowercase a-z/0-9/_ only, no '-').
			Key:         "template",
			Name:        "Template Job",
			Description: "Starter job: GETs a configurable URL on a schedule.",
			Icon:        "⏱️",
			DefaultCron: "0 * * * *",
		},
		Configs: entity.StructToConfigs(Config{}),
		Run:     Run,
	}
}
