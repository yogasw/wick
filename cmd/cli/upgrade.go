package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const wickModule = "github.com/yogasw/wick"

func upgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade wick dependency to latest, tidy, then run dev",
		RunE: func(c *cobra.Command, args []string) error {
			return runUpgrade()
		},
	}
}

func runUpgrade() error {
	latest, err := fetchLatestWickVersion()
	if err != nil {
		return fmt.Errorf("fetch latest: %w", err)
	}

	depVersion, depErr := readWickDepVersion()
	hasGoMod := depErr == nil

	binVersion := AppVersion
	fmt.Printf("cli binary: %s\n", binVersion)
	if hasGoMod {
		fmt.Printf("go.mod dep: %s\n", depVersion)
	}
	fmt.Printf("latest:     %s\n", latest)

	binStale := binVersion != latest && binVersion != "dev"
	depStale := hasGoMod && depVersion != latest

	if !binStale && !depStale && binVersion != "dev" {
		fmt.Println("already on latest")
		return nil
	}

	reader := bufio.NewReader(os.Stdin)

	if binStale || binVersion == "dev" {
		fmt.Printf("upgrade cli binary %s -> %s? [y/N]: ", binVersion, latest)
		ans, _ := reader.ReadString('\n')
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans == "y" || ans == "yes" {
			if err := installCLI(latest); err != nil {
				return err
			}
		} else {
			fmt.Println("cli upgrade skipped")
		}
	}

	if !hasGoMod {
		return nil
	}

	if !depStale {
		return nil
	}

	fmt.Printf("upgrade go.mod dep %s -> %s? [y/N]: ", depVersion, latest)
	ans, _ := reader.ReadString('\n')
	ans = strings.TrimSpace(strings.ToLower(ans))
	if ans != "y" && ans != "yes" {
		fmt.Println("dep upgrade skipped")
		return nil
	}

	if err := execCmd(fmt.Sprintf("go get %s@%s", wickModule, latest)); err != nil {
		return err
	}
	if err := execCmd("go mod tidy"); err != nil {
		return err
	}
	return runTask("dev")
}

func installCLI(version string) error {
	cmd := fmt.Sprintf("go install %s@%s", wickModule, version)

	exe, err := os.Executable()
	if err != nil {
		return execCmd(cmd)
	}

	// Windows can't overwrite a running exe, but can rename it.
	// Rename current binary so go install can write the new one.
	old := exe + ".old"
	if err := os.Rename(exe, old); err != nil {
		return execCmd(cmd)
	}

	if err := execCmd(cmd); err != nil {
		_ = os.Rename(old, exe) // restore on failure
		return fmt.Errorf("install cli: %w", err)
	}

	_ = os.Remove(old)
	fmt.Printf("cli binary upgraded to %s\n", version)
	return nil
}

func readWickDepVersion() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("go.mod not found in current directory")
	}
	re := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(wickModule) + `\s+(\S+)`)
	m := re.FindStringSubmatch(string(data))
	if len(m) < 2 {
		return "", fmt.Errorf("%s not found in go.mod require block", wickModule)
	}
	return m[1], nil
}

func fetchLatestWickVersion() (string, error) {
	goVer, goErr := fetchFromGoProxy()
	ghVer, ghErr := fetchFromGitHub()

	if goErr != nil && ghErr != nil {
		return "", fmt.Errorf("go proxy: %w; github: %v", goErr, ghErr)
	}
	if goErr != nil {
		return ghVer, nil
	}
	if ghErr != nil {
		return goVer, nil
	}
	if semverGT(ghVer, goVer) {
		return ghVer, nil
	}
	return goVer, nil
}

func fetchFromGoProxy() (string, error) {
	resp, err := http.Get("https://proxy.golang.org/" + wickModule + "/@latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var info struct {
		Version string `json:"Version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	if info.Version == "" {
		return "", fmt.Errorf("empty version")
	}
	return info.Version, nil
}

func fetchFromGitHub() (string, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/yogasw/wick/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var info struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	if info.TagName == "" {
		return "", fmt.Errorf("empty tag")
	}
	return info.TagName, nil
}

// semverGT reports whether a > b for vX.Y.Z strings.
func semverGT(a, b string) bool {
	parse := func(v string) [3]int {
		v = strings.TrimPrefix(v, "v")
		parts := strings.SplitN(v, ".", 3)
		var out [3]int
		for i, p := range parts {
			if i >= 3 {
				break
			}
			out[i], _ = strconv.Atoi(p)
		}
		return out
	}
	pa, pb := parse(a), parse(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}
