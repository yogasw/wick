package plugin

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ConfigMaxRSSMB is the service config key that caps a plugin's resident
// memory: past it the supervisor restarts the process. A service opts in by
// declaring the key in its Configs (the admin edits the value); 0 or empty
// means no cap.
const ConfigMaxRSSMB = "wick_max_rss_mb"

// memCheckEvery is how often a capped service's memory is read.
var memCheckEvery = time.Second

// pids maps a live process's socket to its pid, so the supervisor can read
// its memory without the spawn signature carrying it.
var pids sync.Map // socket → int

// rssOfSocket returns the resident memory of the process serving socket, in
// bytes (0 when unknown).
var rssOfSocket = func(socket string) int64 {
	v, ok := pids.Load(socket)
	if !ok {
		return 0
	}
	return rssOfPID(v.(int))
}

// rssOfPID reads VmRSS from /proc (Linux); other platforms report 0, which
// never trips the cap.
func rssOfPID(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			f := strings.Fields(rest)
			if len(f) == 0 {
				return 0
			}
			kb, err := strconv.ParseInt(f[0], 10, 64)
			if err != nil {
				return 0
			}
			return kb << 10
		}
	}
	return 0
}

// maxRSSFrom reads ConfigMaxRSSMB from a service's config, in bytes.
func maxRSSFrom(cfg map[string]string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(cfg[ConfigMaxRSSMB]), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n << 20
}
