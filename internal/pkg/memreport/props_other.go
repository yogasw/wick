//go:build !linux

package memreport

import (
	"errors"
	"time"
)

// Props mirrors the Linux shape so callers compile everywhere; reading
// them needs /proc.
type Props struct {
	PID           int       `json:"pid"`
	PPID          int       `json:"ppid"`
	Name          string    `json:"name"`
	State         string    `json:"state,omitempty"`
	Exe           string    `json:"exe,omitempty"`
	ExeDeleted    bool      `json:"exe_deleted,omitempty"`
	Cmdline       string    `json:"cmdline,omitempty"`
	Cwd           string    `json:"cwd,omitempty"`
	OSUser        string    `json:"os_user,omitempty"`
	Threads       int       `json:"threads,omitempty"`
	RSSBytes      uint64    `json:"rss_bytes"`
	StartedAt     time.Time `json:"started_at,omitzero"`
	Version       string    `json:"version,omitempty"`
	VersionSource string    `json:"version_source,omitempty"`
}

// ReadProps is only implemented where /proc exists.
func ReadProps(pid int) (Props, error) {
	return Props{}, errors.New("process properties are only available on Linux")
}
