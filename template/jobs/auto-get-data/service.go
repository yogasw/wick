package autogetdata

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// fetchRemote performs a GET against url with a 10s timeout and
// returns the response body size. Kept package-private — Run is the
// only exported surface.
func fetchRemote(ctx context.Context, url string) (int, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read body: %w", err)
	}
	return len(data), nil
}
