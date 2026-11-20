package upgrade

import (
	"os"
	"os/exec"
	"sync"

	"github.com/rs/zerolog/log"
	wickenv "github.com/yogasw/wick/internal/pkg/env"
)

// tableflip launches a successor in its parent's session. Android may reclaim
// that child with the parent during a Termux handover, before the Go program
// reaches its first application log. Start it through setsid instead; inherited
// descriptors and tableflip's readiness pipe survive exec unchanged.
var successorArgsMu sync.Mutex

func spawnInOwnSession(fn func() error) error {
	termux := wickenv.IsTermux()
	log.Info().Bool("termux", termux).Msg("upgrade: preparing successor spawn")
	if !termux {
		return fn()
	}

	setsid, err := exec.LookPath("setsid")
	if err != nil {
		log.Warn().Err(err).Msg("upgrade: setsid unavailable; successor stays in the current Termux session")
		return fn()
	}

	successorArgsMu.Lock()
	defer successorArgsMu.Unlock()
	if len(os.Args) == 0 {
		return fn()
	}
	args := append([]string(nil), os.Args...)
	os.Args = append([]string{setsid, args[0]}, args[1:]...)
	defer func() { os.Args = args }()

	log.Info().Str("setsid", setsid).Msg("upgrade: starting successor in its own Termux session")
	return fn()
}
