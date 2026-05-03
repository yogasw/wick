package cli

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// skillLabels maps skill folder name → task description shown in AGENTS.md.
// Extend when a new skill is bundled.
var skillLabels = map[string]string{
	"tool-module":      "Create/edit a tool or job (`tools/`, `jobs/`)",
	"connector-module": "Create/edit a connector (`connectors/`)",
	"design-system":    "UI styling, colors, spacing, components",
	"config-tags":      "Adding/editing `wick:\"...\"` config fields — widget types, modifiers, key derivation",
}

func skillCmd(tpl, designSystem embed.FS) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage AI agent skills in the current project",
	}
	cmd.AddCommand(skillSyncCmd(tpl, designSystem))
	cmd.AddCommand(skillListCmd(tpl, designSystem))
	return cmd
}

func skillListCmd(tpl, designSystem embed.FS) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List skills shipped with this wick version",
		RunE: func(c *cobra.Command, args []string) error {
			names := availableSkills(tpl, designSystem)
			for _, n := range names {
				fmt.Println(n)
			}
			return nil
		},
	}
}

func skillSyncCmd(tpl, designSystem embed.FS) *cobra.Command {
	return &cobra.Command{
		Use:   "sync [name...]",
		Short: "Replace skills in ./.claude/skills/ with the bundled versions (all if no name given)",
		RunE: func(c *cobra.Command, args []string) error {
			all := availableSkills(tpl, designSystem)
			targets := args
			if len(targets) == 0 {
				targets = all
			}
			known := map[string]bool{}
			for _, n := range all {
				known[n] = true
			}
			for _, t := range targets {
				if !known[t] {
					return fmt.Errorf("unknown skill %q (available: %s)", t, strings.Join(all, ", "))
				}
			}
			for _, t := range targets {
				if err := syncSkill(tpl, designSystem, t); err != nil {
					return fmt.Errorf("sync %s: %w", t, err)
				}
				fmt.Printf("synced .claude/skills/%s\n", t)
			}
			if updated, err := syncAgentsSkillTable(tpl, all); err != nil {
				return fmt.Errorf("sync AGENTS.md: %w", err)
			} else if updated {
				fmt.Println("updated AGENTS.md skill table")
			}
			return nil
		},
	}
}

func availableSkills(tpl, designSystem embed.FS) []string {
	var names []string
	if entries, err := fs.ReadDir(tpl, "template/.claude/skills"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
	}
	if entries, err := fs.ReadDir(designSystem, ".claude/skills"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
	}
	return dedupe(names)
}

func syncSkill(tpl, designSystem embed.FS, name string) error {
	tplRoot := "template/.claude/skills/" + name
	if _, err := fs.Stat(tpl, tplRoot); err == nil {
		return copyEmbedDir(tpl, tplRoot, filepath.Join(".claude", "skills", name))
	}
	dsRoot := ".claude/skills/" + name
	if _, err := fs.Stat(designSystem, dsRoot); err == nil {
		return copyEmbedDir(designSystem, dsRoot, filepath.Join(".claude", "skills", name))
	}
	return fmt.Errorf("skill %q not bundled", name)
}

func copyEmbedDir(efs embed.FS, srcRoot, dstRoot string) error {
	if err := os.RemoveAll(dstRoot); err != nil {
		return err
	}
	return fs.WalkDir(efs, srcRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, srcRoot), "/")
		dst := dstRoot
		if rel != "" {
			dst = filepath.Join(dstRoot, rel)
		}
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := efs.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

var (
	skillHeaderRe = regexp.MustCompile(`^\|\s*Task\s*\|\s*Skill\s*\|\s*$`)
	skillSepRe    = regexp.MustCompile(`^\|\s*-+\s*\|\s*-+\s*\|\s*$`)
	skillRowRe    = regexp.MustCompile(`\]\(\./\.claude/skills/[^/)]+/SKILL\.md\)`)
)

// syncAgentsSkillTable rewrites the Task/Skill table in ./AGENTS.md so it lists
// the bundled skills. Skipped (returns updated=false) when:
//   - AGENTS.md doesn't exist
//   - no `| Task | Skill |` table found
//   - any existing body row doesn't link to ./.claude/skills/*/SKILL.md (treated
//     as user-customized)
func syncAgentsSkillTable(tpl embed.FS, bundled []string) (bool, error) {
	const path = "AGENTS.md"
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			data, ferr := freshAgentsMd(tpl, bundled)
			if ferr != nil {
				return false, ferr
			}
			return true, os.WriteFile(path, data, 0o644)
		}
		return false, err
	}
	lines := strings.Split(string(raw), "\n")

	header := -1
	for i, l := range lines {
		if skillHeaderRe.MatchString(l) && i+1 < len(lines) && skillSepRe.MatchString(lines[i+1]) {
			header = i
			break
		}
	}
	if header < 0 {
		return false, nil
	}
	bodyStart := header + 2
	bodyEnd := bodyStart
	for bodyEnd < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[bodyEnd]), "|") {
		bodyEnd++
	}

	for i := bodyStart; i < bodyEnd; i++ {
		if !skillRowRe.MatchString(lines[i]) {
			return false, nil
		}
	}

	var rows []string
	for _, name := range bundled {
		label, ok := skillLabels[name]
		if !ok {
			label = name
		}
		rows = append(rows, fmt.Sprintf("| %s | [`%s`](./.claude/skills/%s/SKILL.md) |", label, name, name))
	}

	out := append([]string{}, lines[:bodyStart]...)
	out = append(out, rows...)
	out = append(out, lines[bodyEnd:]...)
	newContent := strings.Join(out, "\n")
	if newContent == string(raw) {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(newContent), 0o644)
}

// freshAgentsMd returns the initial AGENTS.md content for a project that has
// none yet. Prefers the bundled template/AGENTS.md verbatim (richer: layout,
// commands, naming rules) and falls back to a minimal skill-table stub.
func freshAgentsMd(tpl embed.FS, bundled []string) ([]byte, error) {
	if data, err := tpl.ReadFile("template/AGENTS.md"); err == nil {
		return data, nil
	}
	var rows []string
	for _, name := range bundled {
		label, ok := skillLabels[name]
		if !ok {
			label = name
		}
		rows = append(rows, fmt.Sprintf("| %s | [`%s`](./.claude/skills/%s/SKILL.md) |", label, name, name))
	}
	stub := "# Agent Guide\n\n" +
		"This repo ships AI agent skills. Invoke the matching skill before touching code.\n\n" +
		"## Skills\n\n" +
		"| Task | Skill |\n" +
		"|------|-------|\n" +
		strings.Join(rows, "\n") + "\n"
	return []byte(stub), nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
