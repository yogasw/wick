package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yogasw/wick/internal/agents/schedule"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// EventsPublishMethod is the Invoke name a plugin publishes an event with:
// {"topic": "<topic>", "payload": <any JSON>}. The topic is prefixed with the
// plugin's own key, so a plugin can only wake watches subscribed to its own
// events (wake_on "<key>:<topic>"). The watches run as their owners; the
// plugin needs no acting user and learns only how many were woken.
const EventsPublishMethod = "events.publish"

func init() {
	RegisterMethod(EventsPublishMethod, wickplugin.ScopeHostEventsPublish, false, publishEvent)
}

type publishArgs struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
}

func publishEvent(_ context.Context, in InvokeCall) (json.RawMessage, error) {
	var a publishArgs
	if len(in.Args) > 0 {
		if err := json.Unmarshal(in.Args, &a); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "events.publish args: %v", err)
		}
	}
	topic := strings.TrimSpace(a.Topic)
	if topic == "" {
		return nil, status.Error(codes.InvalidArgument, `events.publish: topic is required, e.g. {"topic":"group/repo","payload":{…}}`)
	}
	full := in.PluginKey + ":" + topic
	payload := []byte(a.Payload)
	if len(payload) == 0 {
		payload = []byte("null")
	}
	n, err := schedule.PublishEvent(full, payload)
	if errors.Is(err, schedule.ErrNoWakeRunner) {
		return nil, status.Error(codes.Unavailable, "events.publish: no schedule runner in this wick process")
	}
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "events.publish: %v", err)
	}
	return json.Marshal(map[string]any{"topic": full, "woken": n})
}
