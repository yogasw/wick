// Package slack — assistant_thread.go: answers assistant_thread_started (a
// user opened the agent's split view in Slack) with the agent's suggested
// prompts.

package slack

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// PromptsFn returns the prompts a new assistant thread is offered.
type PromptsFn func() []SuggestedPrompt

// SetPromptsFn wires the prompt source (nil = offer none).
func (s *Channel) SetPromptsFn(fn PromptsFn) {
	s.cfgMu.Lock()
	s.promptsFn = fn
	s.cfgMu.Unlock()
}

func (s *Channel) handleAssistantThreadStarted(ctx context.Context, ev *slackevents.AssistantThreadStartedEvent) {
	s.cfgMu.Lock()
	fn, api := s.promptsFn, s.api
	s.cfgMu.Unlock()
	th := ev.AssistantThread
	if fn == nil || api == nil || th.ChannelID == "" || th.ThreadTimeStamp == "" {
		return
	}
	prompts := fn()
	if len(prompts) == 0 {
		return
	}
	params := slackgo.AssistantThreadsSetSuggestedPromptsParameters{
		ChannelID: th.ChannelID, ThreadTS: th.ThreadTimeStamp,
	}
	for i, p := range prompts {
		if i == MaxSuggestedPrompts {
			break
		}
		params.Prompts = append(params.Prompts, slackgo.AssistantThreadsPrompt{Title: p.Title, Message: p.Message})
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := api.SetAssistantThreadsSuggestedPromptsContext(cctx, params); err != nil {
		log.Warn().Str("channel", "slack").Str("slack_channel", th.ChannelID).Err(err).Msg("set suggested prompts failed")
	}
}
