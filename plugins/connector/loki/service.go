package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

type queryParams struct {
	Base      string // Grafana base URL
	UID       string // Loki datasource UID
	Query     string
	StartMs   int64 // range start, Unix ms
	EndMs     int64 // range end, Unix ms
	Limit     int
	Direction string
}

func validateQuery(c *connector.Ctx) (queryParams, error) {
	q := strings.TrimSpace(c.Input("query"))
	if q == "" {
		return queryParams{}, errors.New("query is required")
	}

	limit := c.InputInt("limit")
	if limit <= 0 {
		limit = 100
	}
	if limit > 5000 {
		limit = 5000
	}

	direction := strings.TrimSpace(c.Input("direction"))
	if direction == "" {
		direction = "backward"
	}

	base := strings.TrimRight(strings.TrimSpace(c.Cfg("base_url")), "/")
	if base == "" {
		return queryParams{}, errors.New("base_url is not configured")
	}
	uid := strings.TrimSpace(c.Cfg("datasource_uid"))
	if uid == "" {
		return queryParams{}, errors.New("datasource_uid is not configured")
	}

	return queryParams{
		Base:      base,
		UID:       uid,
		Query:     q,
		StartMs:   resolveTimeMs(c.Input("start"), -time.Hour),
		EndMs:     resolveTimeMs(c.Input("end"), 0),
		Limit:     limit,
		Direction: direction,
	}, nil
}

// resolveTimeMs is resolveTime's Unix-millisecond sibling for the /api/ds/query
// from/to fields (Grafana expects epoch ms). Accepts RFC3339, a Unix ns string,
// or empty (now + offset).
func resolveTimeMs(input string, offsetFromNow time.Duration) int64 {
	input = strings.TrimSpace(input)
	if input == "" {
		return time.Now().Add(offsetFromNow).UnixMilli()
	}
	if looksLikeUnixNano(input) {
		if ns, err := strconv.ParseInt(input, 10, 64); err == nil {
			return ns / 1_000_000
		}
	}
	if t, err := time.Parse(time.RFC3339, input); err == nil {
		return t.UnixMilli()
	}
	return time.Now().Add(offsetFromNow).UnixMilli()
}

func validateLabelValues(c *connector.Ctx) (string, error) {
	label := strings.TrimSpace(c.Input("label"))
	if label == "" {
		return "", errors.New("label is required")
	}
	u, err := resourceURL(c, "label/"+url.PathEscape(label)+"/values")
	if err != nil {
		return "", err
	}
	return withLabelWindow(c, u), nil
}

// withLabelWindow appends a start/end time range to a label-discovery URL,
// read from the op's optional start/end inputs (default: last 6h → now).
// Grafana proxies /labels and /label/<x>/values to Loki, and some Loki
// versions/configs reject a range-less label query with a 500
// (plugin.downstreamError) — newer ones default to a window, older ones don't.
// Always sending a window makes label discovery work the same across versions;
// the inputs let the operator widen it or inspect a past period. 6h default
// (wider than query's 1h) so labels that only appear intermittently still
// surface without any input.
func withLabelWindow(c *connector.Ctx, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	q.Set("start", resolveTime(c.Input("start"), -6*time.Hour))
	q.Set("end", resolveTime(c.Input("end"), 0))
	u.RawQuery = q.Encode()
	return u.String()
}

func resourceURL(c *connector.Ctx, path string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(c.Cfg("base_url")), "/")
	if base == "" {
		return "", errors.New("base_url is not configured")
	}
	uid := strings.TrimSpace(c.Cfg("datasource_uid"))
	if uid == "" {
		return "", errors.New("datasource_uid is not configured")
	}
	return fmt.Sprintf("%s/api/datasources/uid/%s/resources/%s", base, uid, path), nil
}

// resolveTime converts to Unix nanosecond string for Loki.
// Accepts RFC3339, a Unix nanosecond string, or empty (falls back to now+offset).
func resolveTime(input string, offsetFromNow time.Duration) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return strconv.FormatInt(time.Now().Add(offsetFromNow).UnixNano(), 10)
	}
	if looksLikeUnixNano(input) {
		return input
	}
	if t, err := time.Parse(time.RFC3339, input); err == nil {
		return strconv.FormatInt(t.UnixNano(), 10)
	}
	// Unknown format — pass through and let Loki validate.
	return input
}

// looksLikeUnixNano is a heuristic: all digits and long enough to be nanoseconds.
func looksLikeUnixNano(s string) bool {
	if len(s) < 15 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
