package claude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EncodeWorkspace is the folder name claude gives a cwd under
// `<config dir>/projects/`: every character that is not an ASCII letter or
// digit becomes '-'. So /home/u/.wick/projects/<id>/files is kept at
// `-home-u--wick-projects-<id>-files`, which is where `--resume` finds the
// transcripts and the auto-memory of a conversation run in that folder.
func EncodeWorkspace(cwd string) string {
	b := []byte(cwd)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

// ForgetWorkspace removes what claude keeps for cwd under configDir: the
// `projects/<encoded cwd>` folder and the folders of cwd's subdirectories
// (`<encoded cwd>-*`, an agent that cd'ed into a repo under it). Only an
// exact name or that name followed by '-' matches, so forgetting
// `.../<id>-files` never touches `.../<id2>-files` or `.../<id>-filesX`.
//
// Callers pass only folders wick owns — a project's managed cwd. A custom
// path belongs to the user, and so does claude's history of it.
func ForgetWorkspace(configDir, cwd string) error {
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if configDir == "" || cwd == "" || cwd == "." || cwd == string(filepath.Separator) {
		return fmt.Errorf("claude: refusing to forget workspace %q in %q", cwd, configDir)
	}
	root := filepath.Join(configDir, "projects")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	name := EncodeWorkspace(cwd)
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || (e.Name() != name && !strings.HasPrefix(e.Name(), name+"-")) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
