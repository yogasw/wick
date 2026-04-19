package main

import (
	"embed"
	"strings"

	"github.com/yogasw/wick/cmd/cli"
)

//go:embed all:template
var templateFS embed.FS

//go:embed all:.claude/skills
var skillsFS embed.FS

//go:embed VERSION
var version string

func main() {
	cli.AppVersion = "v" + strings.TrimSpace(version)
	cli.Execute(templateFS, skillsFS)
}
