package slack

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// slackHTTPClient bounds every Slack Web API call the bot makes. slack-go's
// default client has no timeout at all, so during a network stall a call
// made while handling an event (users.info for the identity gate, a
// reaction) could hang for many minutes — and with it the socket's event
// loop, its reconnect, and every Reload waiting for the run to end.
//
// The bounds sit on the connection and the response headers, not on the
// whole request, so a long file upload or download still completes.
var slackHTTPClient = &http.Client{Transport: newSlackTransport()}

func newSlackTransport() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = 10 * time.Second
	t.ResponseHeaderTimeout = 30 * time.Second
	return t
}

// newBotAPI builds the Web API client of an instance with the bounded
// HTTP client.
func newBotAPI(botToken, appToken string) *slackgo.Client {
	return slackgo.New(botToken,
		slackgo.OptionAppLevelToken(appToken),
		slackgo.OptionHTTPClient(slackHTTPClient))
}

// Vars for tests.
var (
	// reloadWaitTimeout bounds how long Reload waits for the previous run
	// to end. A run stuck in a handler must not keep the bot offline: its
	// context is already cancelled, so it ends on its own once unblocked,
	// and the new run starts without it.
	reloadWaitTimeout = 15 * time.Second
	// socketStuckAfter is how long a socket may stay away from "connected"
	// before the liveness watchdog restarts the instance.
	socketStuckAfter = 2 * time.Minute
	// socketLivenessTick is how often the watchdog looks at the state.
	socketLivenessTick = 20 * time.Second
	// eventQueueSize buffers Events API events between the socket loop and
	// the worker that handles them.
	eventQueueSize = 256
)

// waitRuns waits for wg up to d and reports whether it finished.
func waitRuns(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

// watchConnected restarts the instance when its socket has not been
// "connected" for socketStuckAfter. The retry loop in serve covers a socket
// that stops; this covers one that never comes back (a handler or a dial
// stuck, a reconnect backoff that outlived the outage), which otherwise
// leaves the bot silent until someone saves its settings.
func (s *Channel) watchConnected(runCtx context.Context, gen uint64) {
	defer agentchannels.RecoverPanic("slack", "socket liveness")
	t := time.NewTicker(socketLivenessTick)
	defer t.Stop()
	var downSince time.Time
	for {
		select {
		case <-runCtx.Done():
			return
		case now := <-t.C:
			if s.currentRun() != gen {
				return
			}
			state, _ := s.SocketState()
			if state == "connected" {
				downSince = time.Time{}
				continue
			}
			if downSince.IsZero() {
				downSince = now
				continue
			}
			if now.Sub(downSince) < socketStuckAfter {
				continue
			}
			log.Warn().Str("channel", "slack").Str("bot", s.logBot()).Str("state", state).
				Dur("after", now.Sub(downSince)).Msg("socket not connected, restarting")
			s.cfgMu.Lock()
			cfg, pubURL := s.cfg, s.pubURL
			s.cfgMu.Unlock()
			go s.Reload(context.Background(), cfg, pubURL)
			return
		}
	}
}

// startEventWorker handles Events API events in order, off the socket loop,
// so a slow handler never stalls the connection. The worker is not part of
// runWg: Reload must not wait on it. It drains the queue and exits once
// the returned stop func closes it.
func (s *Channel) startEventWorker(ctx context.Context) (chan<- slackevents.EventsAPIEvent, func()) {
	q := make(chan slackevents.EventsAPIEvent, eventQueueSize)
	go func() {
		for ev := range q {
			s.handleQueuedEvent(ctx, ev)
		}
	}()
	var once sync.Once
	return q, func() { once.Do(func() { close(q) }) }
}

func (s *Channel) handleQueuedEvent(ctx context.Context, ev slackevents.EventsAPIEvent) {
	defer agentchannels.RecoverPanic("slack", "socket event")
	s.handleEventsAPI(ctx, ev)
}

// dispatchSocketEvent acks an Events API event and queues it for the
// worker; other socket events are handled inline. A full queue hands the
// event to its own goroutine rather than blocking the socket.
func (s *Channel) dispatchSocketEvent(ctx context.Context, evt socketmode.Event, q chan<- slackevents.EventsAPIEvent) {
	if evt.Type != socketmode.EventTypeEventsAPI || q == nil {
		s.handleSocketEvent(ctx, evt)
		return
	}
	s.ack(evt)
	apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return
	}
	select {
	case q <- apiEvent:
	default:
		log.Warn().Str("channel", "slack").Str("bot", s.logBot()).Msg("event queue full, handling event on its own goroutine")
		go s.handleQueuedEvent(ctx, apiEvent)
	}
}
