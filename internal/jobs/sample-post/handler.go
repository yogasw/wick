// Package samplepost is a sample job that fetches a post from a
// JSONPlaceholder-compatible API and returns the result as markdown.
//
// Shape: one top-level Run func, no constructor, no Handler struct.
// Metadata (Key/Name/Icon/DefaultCron) is declared by the caller of
// app.RegisterJob — this package only carries the runtime logic and
// the Config schema.
package samplepost

import (
	"context"
	"errors"

	"github.com/yogasw/wick/pkg/job"
)

// Run is the job-side RunFunc wick invokes per schedule tick. It
// reads base_url from the active instance's runtime config and hits
// "{base_url}/posts/1", returning the result as markdown.
func Run(ctx context.Context) (string, error) {
	baseURL := job.FromContext(ctx).Cfg("base_url")
	if baseURL == "" {
		return "", errors.New("base_url not configured — set it from /manager/jobs")
	}
	return fetchPost(ctx, baseURL)
}
