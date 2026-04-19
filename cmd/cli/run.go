package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

type wickConfig struct {
	Vars  map[string]any      `yaml:"vars"`
	Tasks map[string]wickTask `yaml:"tasks"`
}

type wickTask struct {
	Cmds []any  `yaml:"cmds"`
	Done string `yaml:"done"`
}

func loadConfig() (*wickConfig, error) {
	data, err := os.ReadFile("wick.yml")
	if err != nil {
		return nil, fmt.Errorf("wick.yml not found")
	}
	var cfg wickConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse wick.yml: %w", err)
	}
	return &cfg, nil
}

func resolveVars(vars map[string]any) map[string]string {
	resolved := map[string]string{}
	for k, v := range vars {
		switch val := v.(type) {
		case string:
			resolved[k] = val
		case map[string]any:
			if archMap, ok := val["$arch"]; ok {
				resolved[k] = resolveArch(archMap)
			}
		}
	}
	return resolved
}

func resolveArch(raw any) string {
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	exact := goos + "/" + goarch
	if v, ok := m[exact]; ok {
		return fmt.Sprint(v)
	}
	if v, ok := m[goos]; ok {
		return fmt.Sprint(v)
	}
	if v, ok := m["default"]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func interpolate(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{."+k+"}}", v)
	}
	return s
}

func runTask(name string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	task, ok := cfg.Tasks[name]
	if !ok {
		return fmt.Errorf("task %q not found in wick.yml", name)
	}
	vars := resolveVars(cfg.Vars)
	var bgProcs []*os.Process
	for _, raw := range task.Cmds {
		proc, err := dispatchCmdBg(raw, vars)
		if err != nil {
			return fmt.Errorf("cmd failed: %w", err)
		}
		if proc != nil {
			bgProcs = append(bgProcs, proc)
		}
	}
	if task.Done != "" {
		fmt.Println("\n" + strings.TrimSpace(task.Done))
	}
	// wait for all background processes
	for _, p := range bgProcs {
		p.Wait()
	}
	return nil
}

func dispatchCmd(raw any, vars map[string]string) error {
	_, err := dispatchCmdBg(raw, vars)
	return err
}

// dispatchCmdBg returns a non-nil *os.Process if the cmd was launched in background
func dispatchCmdBg(raw any, vars map[string]string) (*os.Process, error) {
	switch v := raw.(type) {
	case string:
		cmd := interpolate(v, vars)
		fmt.Printf("> %s\n", cmd)
		return nil, execCmd(cmd)

	case map[string]any:
		if ifMissing, ok := v["if_missing"]; ok {
			return nil, handleIfMissing(ifMissing, v, vars)
		}
		// background cmd: {bg: true, run: "..."}
		if bg, ok := v["bg"]; ok && bg == true {
			cmd := interpolate(fmt.Sprint(v["run"]), vars)
			fmt.Printf("> %s &\n", cmd)
			return startBackground(cmd)
		}
	}
	return nil, fmt.Errorf("unknown cmd: %v", raw)
}

func startBackground(cmd string) (*os.Process, error) {
	parts := strings.Fields(cmd)
	bin, args := resolveLocalBin(parts[0]), parts[1:]
	c := exec.Command(bin, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		return nil, err
	}
	return c.Process, nil
}

// handleIfMissing processes:
//
//	if_missing:
//	  file: bin/tailwindcss   OR   cmd: templ
//	  download:
//	    dest: ...
//	    url: ...
//	  run: go install ...
func handleIfMissing(ifMissing any, v map[string]any, vars map[string]string) error {
	m, ok := ifMissing.(map[string]any)
	if !ok {
		return fmt.Errorf("if_missing must be a map")
	}

	// check condition
	if file, ok := m["file"]; ok {
		path := interpolate(fmt.Sprint(file), vars)
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("  skip: %s already exists\n", path)
			return nil
		}
	} else if cmd, ok := m["cmd"]; ok {
		bin := interpolate(fmt.Sprint(cmd), vars)
		if _, err := exec.LookPath(bin); err == nil {
			fmt.Printf("  skip: %s already in PATH\n", bin)
			return nil
		}
	}

	// run download block if present
	if dl, ok := v["download"]; ok {
		dlMap, ok := dl.(map[string]any)
		if !ok {
			return fmt.Errorf("download must be a map with dest and url")
		}
		dest := interpolate(fmt.Sprint(dlMap["dest"]), vars)
		url := interpolate(fmt.Sprint(dlMap["url"]), vars)
		useCache := true
		if c, ok := dlMap["cache"]; ok {
			if b, ok := c.(bool); ok {
				useCache = b
			}
		}
		fmt.Printf("> download %s\n", dest)
		if useCache {
			return downloadWithCache(dest, url)
		}
		return httpDownload(url, dest)
	}

	// run shell command if present
	if run, ok := v["run"]; ok {
		cmd := interpolate(fmt.Sprint(run), vars)
		fmt.Printf("> %s\n", cmd)
		return execCmd(cmd)
	}

	return fmt.Errorf("if_missing requires either 'download' or 'run'")
}

func wickCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "wick")
	return dir, os.MkdirAll(dir, 0o755)
}

func downloadWithCache(dest, url string) error {
	cacheDir, err := wickCacheDir()
	if err != nil {
		return err
	}
	cached := filepath.Join(cacheDir, filepath.Base(dest))

	if _, err := os.Stat(cached); err != nil {
		fmt.Printf("  downloading %s...\n", url)
		if err := httpDownload(url, cached); err != nil {
			return err
		}
	} else {
		fmt.Printf("  cache hit: %s\n", filepath.Base(dest))
	}

	if err := copyFile(cached, dest); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Chmod(dest, 0o755)
	}
	return nil
}

func httpDownload(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func execCmd(cmd string) error {
	switch {
	case strings.HasPrefix(cmd, "mkdir -p "):
		dirs := strings.Fields(strings.TrimPrefix(cmd, "mkdir -p "))
		for _, d := range dirs {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		return nil

	case strings.HasPrefix(cmd, "chmod +x "):
		if runtime.GOOS == "windows" {
			return nil
		}
		return os.Chmod(strings.TrimSpace(strings.TrimPrefix(cmd, "chmod +x ")), 0o755)
	}

	parts := strings.Fields(cmd)
	bin, args := resolveLocalBin(parts[0]), parts[1:]
	c := exec.Command(bin, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// resolveLocalBin converts bin/foo or ./bin/foo to an absolute path so the OS can find it
func resolveLocalBin(bin string) string {
	if strings.HasPrefix(bin, "bin/") || strings.HasPrefix(bin, "./") {
		if abs, err := filepath.Abs(bin); err == nil {
			return abs
		}
	}
	return bin
}
