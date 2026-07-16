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
	URL       string
	Query     string
	Start     string
	End       string
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

	u, err := resourceURL(c, "query_range")
	if err != nil {
		return queryParams{}, err
	}

	return queryParams{
		URL:       u,
		Query:     q,
		Start:     resolveTime(c.Input("start"), -time.Hour),
		End:       resolveTime(c.Input("end"), 0),
		Limit:     limit,
		Direction: direction,
	}, nil
}

func validateLabelValues(c *connector.Ctx) (string, error) {
	label := strings.TrimSpace(c.Input("label"))
	if label == "" {
		return "", errors.New("label is required")
	}
	return resourceURL(c, "label/"+url.PathEscape(label)+"/values")
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
