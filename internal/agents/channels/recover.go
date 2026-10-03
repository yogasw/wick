package channels

import (
	"runtime/debug"

	"github.com/rs/zerolog/log"
)

// RecoverPanic stops a panic in one channel goroutine from taking the
// whole daemon down. Defer it directly (`defer channels.RecoverPanic(...)`)
// at the top of every goroutine a channel spawns — an event handler, a
// Start loop — so a bug in one bot costs that one event, not every
// session on the box. The stack is logged so the bug is still found.
func RecoverPanic(channel, where string) {
	if r := recover(); r != nil {
		log.Error().Str("channel", channel).Str("where", where).
			Interface("panic", r).Str("stack", string(debug.Stack())).
			Msg("channel goroutine panicked; recovered")
	}
}
