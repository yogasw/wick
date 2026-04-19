package cli

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func initCmd(tpl, skills embed.FS) *cobra.Command {
	var skipSetup bool
	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Create new project in ./<name> (default: myapp)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := "myapp"
			if len(args) == 1 {
				name = args[0]
			}
			if err := scaffold(tpl, name); err != nil {
				return err
			}
			if err := copySkills(skills, name); err != nil {
				return fmt.Errorf("copy skills: %w", err)
			}
			fmt.Printf("created %s/\n", name)

			if skipSetup {
				fmt.Printf("\nnext:\n  cd %s\n  go mod tidy\n  go run . setup\n  go run . dev\n", name)
				return nil
			}

			if err := runIn(name, "go", "mod", "tidy"); err != nil {
				return fmt.Errorf("go mod tidy: %w", err)
			}
			if err := runIn(name, "wick", "setup"); err != nil {
				return fmt.Errorf("wick setup: %w", err)
			}

			fmt.Printf("\nready. run:\n  cd %s\n  go run . dev\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&skipSetup, "skip-setup", false, "skip go mod tidy and make setup")
	return cmd
}

func scaffold(tpl embed.FS, name string) error {
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("directory %q already exists", name)
	}
	return fs.WalkDir(tpl, "template", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return writeEntry(tpl, name, p, d)
	})
}

func writeEntry(tpl embed.FS, name, p string, d fs.DirEntry) error {
	rel := strings.TrimPrefix(strings.TrimPrefix(p, "template"), "/")
	if rel == "" {
		return os.MkdirAll(name, 0o755)
	}
	dst := filepath.Join(name, rel)
	if d.IsDir() {
		return os.MkdirAll(dst, 0o755)
	}
	dst = strings.TrimSuffix(dst, ".tmpl")

	data, err := tpl.ReadFile(p)
	if err != nil {
		return err
	}
	data = rewrite(p, data, name)
	return os.WriteFile(dst, data, 0o644)
}

func rewrite(p string, data []byte, name string) []byte {
	switch {
	case strings.HasSuffix(p, ".go"), strings.HasSuffix(p, "go.mod.tmpl"):
		s := string(data)
		s = strings.ReplaceAll(s, "module template", "module "+name)
		s = strings.ReplaceAll(s, "\"template/", "\""+name+"/")
		return []byte(s)
	case strings.HasSuffix(p, "Makefile"):
		s := string(data)
		s = strings.ReplaceAll(s, "IMAGE            ?= template", "IMAGE            ?= "+name)
		s = strings.ReplaceAll(s, "/template$(EXE)", "/"+name+"$(EXE)")
		return []byte(s)
	}
	return data
}

func copySkills(skills embed.FS, name string) error {
	return fs.WalkDir(skills, ".claude/skills", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(name, p)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := skills.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

func runIn(dir, bin string, args ...string) error {
	fmt.Printf("\n> %s %s\n", bin, strings.Join(args, " "))
	c := exec.Command(bin, args...)
	c.Dir = dir
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
