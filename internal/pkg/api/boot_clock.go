package api

import (
	"time"

	"github.com/rs/zerolog/log"
)

// bootClock logs how long each stretch of server construction took. A
// handover kills a successor that is not ready within the predecessor's
// limit, and the boot log had a silent gap of about two minutes with nothing
// saying which step it belonged to. One line per checkpoint names it.
type bootClock struct {
	start, last time.Time
}

func newBootClock() *bootClock {
	now := time.Now()
	return &bootClock{start: now, last: now}
}

// mark logs the time since the previous mark under the name of the step that
// just finished.
func (c *bootClock) mark(step string) {
	now := time.Now()
	log.Info().Str("step", step).
		Dur("took", now.Sub(c.last)).
		Dur("since_clock", now.Sub(c.start)).
		Msg("boot: step timing")
	c.last = now
}
