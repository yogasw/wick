package main

import (
	"fmt"

	"github.com/yogasw/wick/pkg/connector"
)

// Small named adapters keep the operation catalog declarative and ensure
// all filesystem calls pass through fileAction's authorization wrapper.
func fileStat(_ Policy, name string, _ *connector.Ctx) (any, error) {
	return statFile(name)
}

func fileDelete(_ Policy, name string, _ *connector.Ctx) (any, error) {
	if err := removeFile(name, false); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

func directoryCreate(_ Policy, name string, _ *connector.Ctx) (any, error) {
	if err := mkdir(name); err != nil {
		return nil, err
	}
	return map[string]bool{"created": true}, nil
}

func directoryDelete(_ Policy, name string, _ *connector.Ctx) (any, error) {
	if err := removeFile(name, true); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

func directoryTree(p Policy, name string, c *connector.Ctx) (any, error) {
	return search(p, name, c, false, true)
}

func searchFiles(p Policy, name string, c *connector.Ctx) (any, error) {
	return search(p, name, c, false, false)
}

func searchContent(p Policy, name string, c *connector.Ctx) (any, error) {
	return search(p, name, c, true, false)
}

func execCommand(c *connector.Ctx) (any, error) {
	return executeCommand(c, false)
}

func execShell(c *connector.Ctx) (any, error) {
	return executeCommand(c, true)
}

// systemDiskUsage requires both system and path-level permission.
func systemDiskUsage(p Policy, name string, _ *connector.Ctx) (any, error) {
	if !contains(p.System.Allow, "disk_usage") {
		return nil, fmt.Errorf("disk_usage denied")
	}
	return diskUsage(name)
}
