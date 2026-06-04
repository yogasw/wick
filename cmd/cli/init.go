package cli

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/safeexec"
)

func initCmd(tpl, designSystem, installScripts embed.FS) *cobra.Command {
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
			for _, skill := range []string{"design-system", "config-tags"} {
				if err := copySkillFromFS(designSystem, ".claude/skills/"+skill, name); err != nil {
					return fmt.Errorf("copy %s skill: %w", skill, err)
				}
			}
			if err := copyInstallScripts(installScripts, name); err != nil {
				return fmt.Errorf("copy install scripts: %w", err)
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
	case strings.HasSuffix(p, "wick.yml"):
		s := string(data)
		s = strings.Replace(s, "name: app", "name: "+name, 1)
		return []byte(s)
	}
	return data
}

// copyInstallScripts drops scripts/install.sh + scripts/install.ps1 into
// the new project root, rewriting the baked APP/REPO so the scaffolded
// scripts target the user's app instead of wick itself. REPO owner is
// left as `owner/<name>` for the user to edit once they create the
// GitHub repo.
func copyInstallScripts(fsys embed.FS, name string) error {
	files := []string{"scripts/install.sh", "scripts/install.ps1"}
	for _, src := range files {
		data, err := fsys.ReadFile(src)
		if err != nil {
			return err
		}
		s := string(data)
		s = strings.ReplaceAll(s, `APP="wick-agent"`, `APP="`+name+`"`)
		s = strings.ReplaceAll(s, `$App   = 'wick-agent'`, `$App   = '`+name+`'`)
		s = strings.ReplaceAll(s, `REPO="yogasw/wick"`, `REPO="owner/`+name+`"`)
		s = strings.ReplaceAll(s, `$Repo  = 'yogasw/wick'`, `$Repo  = 'owner/`+name+`'`)
		dst := filepath.Join(name, filepath.Base(src))
		mode := os.FileMode(0o644)
		if strings.HasSuffix(src, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(dst, []byte(s), mode); err != nil {
			return err
		}
	}
	return nil
}

func copySkillFromFS(fsys embed.FS, src, name string) error {
	return fs.WalkDir(fsys, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(name, p)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := fsys.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

func runIn(dir, bin string, args ...string) error {
	fmt.Printf("\n> %s %s\n", bin, strings.Join(args, " "))
	c := safeexec.Command(bin, args...)
	c.Dir = dir
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
