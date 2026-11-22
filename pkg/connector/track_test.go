package connector

import (
	"strings"
	"testing"
	"time"
)

func TestParseTrack(t *testing.T) {
	if tr, err := ParseTrack(""); tr != nil || err != nil {
		t.Fatalf("empty = %v %v", tr, err)
	}
	tr, err := ParseTrack(`{"status_op":"get_job","params":{"id":"out.id"},"state_path":"state","done":["OK"],"every":"5s"}`)
	if err != nil {
		t.Fatal(err)
	}
	if tr.EveryDuration() != 10*time.Second {
		t.Errorf("every must be floored to 10s, got %s", tr.EveryDuration())
	}
	if tr.TimeoutString() != TrackDefaultTimeout.String() {
		t.Errorf("timeout default = %s", tr.TimeoutString())
	}
	if _, err := ParseTrack(`{"status_op":"get_job"}`); err == nil || !strings.Contains(err.Error(), "state_path") || !strings.Contains(err.Error(), "done") {
		t.Errorf("missing fields must be named, got %v", err)
	}
	if _, err := ParseTrack(`{"status_op":"x","state_path":"s","done":["OK"],"bogus":1}`); err == nil {
		t.Error("unknown field must be rejected")
	}
	if _, err := ParseTrack(`{"status_op":"x","state_path":"s","done":["OK"],"timeout":"soon"}`); err == nil {
		t.Error("bad timeout must be rejected")
	}
}
