package providerstoragesync

import "github.com/yogasw/wick/internal/entity"

// Config keys for this job — also surfaced in the manager Settings page
// under owner = Key ("provider-storage-sync").
const (
	// CfgWatcherStatus toggles the realtime fsnotify watcher. When on,
	// file changes sync to DB immediately (debounced) and the cron tick
	// becomes a safety net for events the kernel dropped. When off,
	// only the cron tick runs.
	CfgWatcherStatus = "watcher_status"

	// CfgWatcherDebounceMs is the per-path debounce window in
	// milliseconds. Lower = faster sync but more DB writes during
	// editor save bursts. Default 1000.
	CfgWatcherDebounceMs = "watcher_debounce_ms"
)

// WatcherStatus is the seed row for CfgWatcherStatus. Manual entity.Config
// (rather than StructToConfigs) so other packages can import the var and
// reference the same Description in tooltips without reparsing tags.
var WatcherStatus = entity.Config{
	Key:         CfgWatcherStatus,
	Value:       "true",
	Type:        "bool",
	Description: "Realtime filesystem watcher. When on, file changes sync to DB instantly (debounced). When off, only the cron tick runs.",
}

// WatcherDebounceMs is the seed row for CfgWatcherDebounceMs.
var WatcherDebounceMs = entity.Config{
	Key:         CfgWatcherDebounceMs,
	Value:       "1000",
	Type:        "number",
	Description: "Debounce window for watcher events in milliseconds. Default 1000.",
}
