package storage

import (
	"fmt"
	"regexp"
	"strings"
)

// projectNameRe and presetNameRe enforce path-traversal-safe
// identifiers. Project / preset names are human-typed (underscores and
// hyphens OK).
//
// sessionIDRe additionally allows `.` because Slack thread_ts looks
// like "1715167891.234567".
var (
	projectNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	sessionIDRe   = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	presetNameRe  = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// ValidateProjectName rejects names containing path separators, dots,
// or characters outside [A-Za-z0-9_-]. See agents-design.md §15.7.
func ValidateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("project name is empty")
	}
	if !projectNameRe.MatchString(name) {
		return fmt.Errorf("invalid project name %q (allowed: [A-Za-z0-9_-])", name)
	}
	return nil
}

// ValidateSessionID accepts the dotted Slack thread_ts form in addition
// to the project-name charset. Leading dots and `..` are still rejected.
func ValidateSessionID(id string) error {
	if id == "" {
		return fmt.Errorf("session id is empty")
	}
	if strings.HasPrefix(id, ".") || strings.Contains(id, "..") {
		return fmt.Errorf("invalid session id %q", id)
	}
	if !sessionIDRe.MatchString(id) {
		return fmt.Errorf("invalid session id %q (allowed: [A-Za-z0-9._-])", id)
	}
	return nil
}

// ValidatePresetName mirrors project-name rules.
func ValidatePresetName(name string) error {
	if name == "" {
		return fmt.Errorf("preset name is empty")
	}
	if !presetNameRe.MatchString(name) {
		return fmt.Errorf("invalid preset name %q (allowed: [A-Za-z0-9_-])", name)
	}
	return nil
}
