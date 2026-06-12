package phoenix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// repo.go owns every outbound network call to Phoenix. All traffic goes
// through the single GraphQL endpoint (BaseURL + "/graphql"); handlers in
// connector.go compose these fetchers with the pure parsers in service.go
// but never reach for net/http themselves.
//
// http.NewRequestWithContext is mandatory: the request MUST inherit
// c.Context() so MCP cancellations (client disconnect, deadline) abort the
// upstream call instead of leaking the goroutine.

// graphQLEnvelope is the standard GraphQL response wrapper. Phoenix returns
// 200 OK even for query errors, surfacing them in the errors array, so a
// non-empty errors slice is treated as a failure regardless of status code.
type graphQLEnvelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// graphQL posts a query + variables to Phoenix and returns the raw `data`
// payload for the caller to decode into a query-specific struct.
func graphQL(c *connector.Ctx, query string, variables map[string]any) (json.RawMessage, error) {
	base := strings.TrimRight(strings.TrimSpace(c.Cfg("base_url")), "/")
	if base == "" {
		return nil, fmt.Errorf("base_url is not configured")
	}
	token := strings.TrimSpace(c.Cfg("api_token"))
	if token == "" {
		return nil, fmt.Errorf("api_token is not configured")
	}

	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return nil, fmt.Errorf("encode graphql request: %w", err)
	}

	req, err := http.NewRequestWithContext(c.Context(), http.MethodPost, base+"/graphql", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call phoenix: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("phoenix %d: %s", resp.StatusCode, truncate(msg, 300))
	}

	var env graphQLEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, truncate(string(raw), 200))
	}
	if len(env.Errors) > 0 {
		msgs := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			msgs = append(msgs, e.Message)
		}
		return nil, fmt.Errorf("phoenix graphql: %s", strings.Join(msgs, "; "))
	}
	return env.Data, nil
}

// ── Wire types ───────────────────────────────────────────────────────────
// These mirror the GraphQL response shapes. `attributes` arrives as a
// stringified JSON blob with a NESTED shape (attrs.llm.input_messages[]…),
// decoded lazily by service.go — never as flat dotted keys.

type wireSession struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	NumTraces int    `json:"numTraces"`
	StartTime string `json:"startTime"`
}

type wireTrace struct {
	ID      string `json:"id"`
	TraceID string `json:"traceId"`
	RootSpan struct {
		ID     string `json:"id"`
		SpanID string `json:"spanId"`
	} `json:"rootSpan"`
}

// wireSpan is the per-span node returned by the trace-spans and span-detail
// queries. spanKind is lowercase over GraphQL ("llm"/"chain"/"agent").
type wireSpan struct {
	ID              string  `json:"id"`
	SpanID          string  `json:"spanId"`
	Name            string  `json:"name"`
	SpanKind        string  `json:"spanKind"`
	StartTime       string  `json:"startTime"`
	StatusCode      string  `json:"statusCode"`
	LatencyMs       float64 `json:"latencyMs"`
	TokenCountTotal int     `json:"tokenCountTotal"`
	Attributes      string  `json:"attributes"`
	ParentID        *string `json:"parentId"`
	Input           struct {
		Value string `json:"value"`
	} `json:"input"`
	Output struct {
		Value string `json:"value"`
	} `json:"output"`
	Metadata string `json:"metadata"`
	Trace    struct {
		ID      string `json:"id"`
		TraceID string `json:"traceId"`
	} `json:"trace"`
	CostSummary struct {
		Total struct {
			Cost float64 `json:"cost"`
		} `json:"total"`
	} `json:"costSummary"`
}

// ── Queries ──────────────────────────────────────────────────────────────

const sessionsQuery = `query($id: ID!, $sessionId: String, $timeRange: TimeRange, $first: Int) {
  node(id: $id) {
    ... on Project {
      sessions(first: $first, sessionId: $sessionId, timeRange: $timeRange,
               sort: {col: startTime, dir: desc}) {
        edges { session: node { id sessionId numTraces startTime } }
      }
    }
  }
}`

const tracesQuery = `query($id: ID!, $first: Int) {
  session: node(id: $id) {
    ... on ProjectSession {
      traces(first: $first) {
        edges { trace: node { id traceId rootSpan { id spanId } } }
      }
    }
  }
}`

const traceSpansQuery = `query($id: ID!) {
  trace: node(id: $id) {
    ... on Trace {
      traceId
      spans(first: 100) {
        edges {
          node {
            id spanId name spanKind startTime latencyMs
            tokenCountTotal statusCode attributes parentId
            input { value }
            output { value }
          }
        }
      }
    }
  }
}`

const appSpansQuery = `query SpansByAppId(
  $id: ID!,
  $first: Int = 30,
  $after: String = null,
  $filterCondition: String = null,
  $rootSpansOnly: Boolean = true,
  $sort: SpanSort = {col: startTime, dir: desc},
  $timeRange: TimeRange
) {
  node(id: $id) {
    ... on Project {
      spans(first: $first, after: $after, sort: $sort,
            rootSpansOnly: $rootSpansOnly, filterCondition: $filterCondition,
            timeRange: $timeRange) {
        edges {
          span: node {
            id spanId spanKind name metadata statusCode startTime
            latencyMs tokenCountTotal
            trace { id traceId }
            input { value: truncatedValue }
            output { value: truncatedValue }
          }
          cursor
        }
        pageInfo { endCursor hasNextPage }
      }
    }
  }
}`

const spanDetailQuery = `query($spanId: ID!) {
  span: node(id: $spanId) {
    __typename
    id
    ... on Span {
      spanId name spanKind startTime
      trace { id traceId }
      tokenCountTotal latencyMs
      input { value }
      output { value }
      attributes
      costSummary { total { cost } }
    }
  }
}`

// ── Fetchers ─────────────────────────────────────────────────────────────

// fetchSessions resolves the Phoenix sessions for a conversation room. The room id
// is matched against ProjectSession.sessionId — room_id itself is numeric and
// unindexed, so it can only be reached through the sessionId argument here,
// never a metadata filterCondition.
func fetchSessions(c *connector.Ctx, projectID, roomID, timeStart string) ([]wireSession, error) {
	data, err := graphQL(c, sessionsQuery, map[string]any{
		"id":        projectID,
		"sessionId": roomID,
		"first":     30,
		"timeRange": map[string]any{"start": timeStart},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Node struct {
			Sessions struct {
				Edges []struct {
					Session wireSession `json:"session"`
				} `json:"edges"`
			} `json:"sessions"`
		} `json:"node"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode sessions: %w", err)
	}
	sessions := make([]wireSession, 0, len(out.Node.Sessions.Edges))
	for _, e := range out.Node.Sessions.Edges {
		sessions = append(sessions, e.Session)
	}
	return sessions, nil
}

func fetchTraces(c *connector.Ctx, sessionNodeID string) ([]wireTrace, error) {
	data, err := graphQL(c, tracesQuery, map[string]any{"id": sessionNodeID, "first": 50})
	if err != nil {
		return nil, err
	}
	var out struct {
		Session struct {
			Traces struct {
				Edges []struct {
					Trace wireTrace `json:"trace"`
				} `json:"edges"`
			} `json:"traces"`
		} `json:"session"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode traces: %w", err)
	}
	traces := make([]wireTrace, 0, len(out.Session.Traces.Edges))
	for _, e := range out.Session.Traces.Edges {
		traces = append(traces, e.Trace)
	}
	return traces, nil
}

func fetchTraceSpans(c *connector.Ctx, traceNodeID string) ([]wireSpan, error) {
	data, err := graphQL(c, traceSpansQuery, map[string]any{"id": traceNodeID})
	if err != nil {
		return nil, err
	}
	var out struct {
		Trace struct {
			Spans struct {
				Edges []struct {
					Node wireSpan `json:"node"`
				} `json:"edges"`
			} `json:"spans"`
		} `json:"trace"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode trace spans: %w", err)
	}
	spans := make([]wireSpan, 0, len(out.Trace.Spans.Edges))
	for _, e := range out.Trace.Spans.Edges {
		spans = append(spans, e.Node)
	}
	return spans, nil
}

// fetchAppSpans pages through root spans matching an app_id metadata filter,
// stopping once maxSpans is reached or Phoenix reports no further pages.
func fetchAppSpans(c *connector.Ctx, projectID, filterCondition, timeStart string, maxSpans int, rootOnly bool) ([]wireSpan, error) {
	const pageSize = 100
	var (
		all   []wireSpan
		after *string
	)
	for len(all) < maxSpans {
		first := min(pageSize, maxSpans-len(all))
		vars := map[string]any{
			"id":              projectID,
			"first":           first,
			"after":           after,
			"filterCondition": filterCondition,
			"rootSpansOnly":   rootOnly,
			"sort":            map[string]any{"col": "startTime", "dir": "desc"},
			"timeRange":       map[string]any{"start": timeStart},
		}
		data, err := graphQL(c, appSpansQuery, vars)
		if err != nil {
			return nil, err
		}
		var out struct {
			Node struct {
				Spans struct {
					Edges []struct {
						Span wireSpan `json:"span"`
					} `json:"edges"`
					PageInfo struct {
						EndCursor   string `json:"endCursor"`
						HasNextPage bool   `json:"hasNextPage"`
					} `json:"pageInfo"`
				} `json:"spans"`
			} `json:"node"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("decode app spans: %w", err)
		}
		edges := out.Node.Spans.Edges
		if len(edges) == 0 {
			break
		}
		for _, e := range edges {
			all = append(all, e.Span)
		}
		if !out.Node.Spans.PageInfo.HasNextPage {
			break
		}
		cursor := out.Node.Spans.PageInfo.EndCursor
		after = &cursor
	}
	return all, nil
}

func fetchSpanDetail(c *connector.Ctx, spanNodeID string) (wireSpan, error) {
	data, err := graphQL(c, spanDetailQuery, map[string]any{"spanId": spanNodeID})
	if err != nil {
		return wireSpan{}, err
	}
	var out struct {
		Span wireSpan `json:"span"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return wireSpan{}, fmt.Errorf("decode span detail: %w", err)
	}
	if out.Span.ID == "" {
		return wireSpan{}, fmt.Errorf("span %q not found", spanNodeID)
	}
	return out.Span, nil
}
