//go:build windows

package main

import (
	"encoding/csv"
	"os"
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/safeexec"
)

// findOwnedBrowserPIDs returns every process whose command line points at a
// user-data dir under root. See the unix implementation for why command line —
// not the recorded PID — is the ownership marker.
//
// Windows has no /proc, so this shells out to WMIC, which is the one built-in
// that reports CommandLine for every process. Output is parsed as CSV because
// command lines contain spaces and quotes that no field-splitting survives.
func findOwnedBrowserPIDs(root string) []ownedProc {
	if root == "" {
		return nil
	}
	cmd := safeexec.Command("wmic", "process", "get", "ProcessId,CommandLine", "/format:csv")
	raw, err := cmd.Output()
	if err != nil {
		return nil
	}
	// WMIC emits a leading blank line and CRLF endings; the CSV reader handles
	// the latter, so only the empty lines need dropping.
	r := csv.NewReader(strings.NewReader(strings.TrimSpace(string(raw))))
	r.FieldsPerRecord = -1 // command lines with commas make the count vary
	records, err := r.ReadAll()
	if err != nil || len(records) == 0 {
		return nil
	}
	// Header is Node,CommandLine,ProcessId — locate the columns by name so a
	// different WMIC column order does not silently read the wrong field.
	cmdCol, pidCol := -1, -1
	for i, h := range records[0] {
		switch strings.TrimSpace(h) {
		case "CommandLine":
			cmdCol = i
		case "ProcessId":
			pidCol = i
		}
	}
	if cmdCol < 0 || pidCol < 0 {
		return nil
	}

	self := os.Getpid()
	var out []ownedProc
	for _, rec := range records[1:] {
		if len(rec) <= cmdCol || len(rec) <= pidCol {
			continue
		}
		udd, ok := extractUserDataDir(rec[cmdCol], root)
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(rec[pidCol]))
		if err != nil || pid <= 0 || pid == self {
			continue
		}
		out = append(out, ownedProc{PID: pid, UserDataDir: udd})
	}
	return out
}

// extractUserDataDir pulls the --user-data-dir value out of a full command
// line and reports whether it lives under root. Windows gives us one flat
// string rather than argv, so the value has to be delimited by hand: it ends at
// the closing quote when quoted (Chrome quotes paths containing spaces) and at
// the next whitespace otherwise.
//
// The root check happens AFTER unquoting — matching the prefix against the raw
// command line would fail on a quoted path, where a `"` sits between the flag
// and the directory.
func extractUserDataDir(cmdline, root string) (string, bool) {
	const flag = "--user-data-dir="
	i := strings.Index(cmdline, flag)
	if i < 0 {
		return "", false
	}
	rest := cmdline[i+len(flag):]
	var dir string
	switch {
	case strings.HasPrefix(rest, `"`):
		if j := strings.Index(rest[1:], `"`); j >= 0 {
			dir = rest[1 : 1+j]
		} else {
			dir = strings.TrimPrefix(rest, `"`)
		}
	default:
		dir = rest
		if j := strings.IndexAny(dir, " \t"); j >= 0 {
			dir = dir[:j]
		}
	}
	if !strings.HasPrefix(dir, root) {
		return "", false
	}
	return dir, true
}
