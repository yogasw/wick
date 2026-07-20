package main

import "testing"

// sampleFrame mirrors a real Grafana /api/ds/query response for a Loki log
// query: one result "A" with a log frame whose columns (data.values) are
// described by schema.fields — labels(object), Time(ms), Line(string),
// tsNs(string ns), id(string).
const sampleFrame = `{
  "results": {
    "A": {
      "status": 200,
      "frames": [
        {
          "schema": {
            "refId": "A",
            "fields": [
              {"name": "labels", "type": "other"},
              {"name": "Time",   "type": "time"},
              {"name": "Line",   "type": "string"},
              {"name": "tsNs",   "type": "string"},
              {"name": "id",     "type": "string"}
            ]
          },
          "data": {
            "values": [
              [{"app": "api", "level": "error"}, {"app": "api", "level": "info"}],
              [1721448238308, 1721448239000],
              ["Job agent threshold executed", "second line"],
              ["1721448238308123456", "1721448239000000000"],
              ["id1", "id2"]
            ]
          }
        }
      ]
    }
  }
}`

func TestParseLogFrames(t *testing.T) {
	res, err := parseLogFrames([]byte(sampleFrame))
	if err != nil {
		t.Fatalf("parseLogFrames: %v", err)
	}
	if res.Count != 2 || len(res.Entries) != 2 {
		t.Fatalf("count = %d, want 2", res.Count)
	}
	e := res.Entries[0]
	if e.Line != "Job agent threshold executed" {
		t.Errorf("Line = %q", e.Line)
	}
	if e.Labels["app"] != "api" || e.Labels["level"] != "error" {
		t.Errorf("Labels = %v", e.Labels)
	}
	if e.Timestamp == "" {
		t.Errorf("Timestamp empty (want RFC3339 from tsNs)")
	}
}

func TestParseLogFrames_PerQueryError(t *testing.T) {
	// A 200 with a per-query error must surface, not silently return 0 rows.
	body := `{"results":{"A":{"error":"parse error: unexpected }","frames":[]}}}`
	if _, err := parseLogFrames([]byte(body)); err == nil {
		t.Fatal("expected an error for a per-query error result")
	}
}

func TestParseLogFrames_MetricFrameSkipped(t *testing.T) {
	// A metric frame (Time + value, no Line) yields no log entries, no error.
	body := `{"results":{"A":{"frames":[{"schema":{"fields":[{"name":"Time"},{"name":"Value"}]},"data":{"values":[[1721448238308],[3.14]]}}]}}}`
	res, err := parseLogFrames([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Count != 0 {
		t.Errorf("count = %d, want 0 for a metric frame", res.Count)
	}
}
