// Package autogetdata is a template job that fetches a remote
// endpoint on a schedule.
//
// Shape: one top-level Run func, no constructor, no Handler struct.
// Metadata (Key/Name/Icon/DefaultCron) is declared by the caller of
// app.RegisterJob — this package only carries the runtime logic and
// the Config schema.
package autogetdata

import (
	"context"
	"errors"
	"fmt"

	"github.com/yogasw/wick/pkg/job"
)

// Run is the job-side RunFunc wick invokes per schedule tick. It
// reads url from the active instance's runtime config, fetches it,
// and returns a short markdown summary.
func Run(ctx context.Context) (string, error) {
	url := job.FromContext(ctx).Cfg("url")
	if url == "" {
		return "", errors.New("url not configured — set it from /manager/jobs")
	}
	n, err := fetchRemote(ctx, url)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("fetched %d bytes from %s", n, url), nil
}
