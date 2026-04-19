package main

import (
	"embed"

	"github.com/yogasw/wick/cmd/cli"
)

//go:embed all:template
var templateFS embed.FS

//go:embed all:.claude/skills
var skillsFS embed.FS

func main() {
	cli.Execute(templateFS, skillsFS)
}
