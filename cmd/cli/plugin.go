package cli

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/safeexec"
)

// pluginBuildTargets is the canonical OS/arch matrix `wick plugin build --all`
// iterates. linux/arm64 is first and mandatory — it is the Termux target the
// whole plugin platform exists to serve.
var pluginBuildTargets = []string{
	"linux/arm64",
	"linux/amd64",
	"darwin/arm64",
	"darwin/amd64",
	"windows/amd64",
}

// pluginKinds are the plugin kinds wick understands. Each maps 1:1 to a source
// folder in a wick-plugins monorepo (connector/, tool/, job/). Only connector
// has a host contract today; tool/job build identically (the binary's own
// --dump-manifest carries its declared kind) and are accepted now so the repo
// layout and CLI are forward-compatible.
var pluginKinds = map[string]bool{
	"connector": true,
	"tool":      true,
	"job":       true,
}

// pluginCmd is the scaffolder-side `wick plugin` group: the PRODUCTION side of
// the plugin platform (build source → release zip). The CONSUMPTION side
// (install / list / enable / disable / remove) lives in the app binary
// (app/plugin_cmd.go) because the plugins dir + enable/disable DB belong to the
// running app, not this dev tool.
func pluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Build wick plugins (connector/tool/job) for release",
		Long: `Production-side tooling for the wick plugin platform.

Run this from a wick-plugins monorepo (one folder per plugin, grouped by kind):

  wick-plugins/
  ├── connector/  { _template/, gmail/, slack/ }
  ├── tool/       (later)
  └── job/        (later)

The consumption side — installing, enabling, disabling plugins — lives in the
app binary itself: '<your-app> plugin install|list|enable|disable|remove'.`,
	}
	cmd.AddCommand(pluginBuildCmd())
	return cmd
}

// pluginBuildCmd compiles plugin binaries from a wick-plugins-style monorepo
// (<kind>/<name>/main.go each a `package main` that calls plugin.Serve) and
// packs each built binary plus its generated plugin.json into a release zip:
//
//	<name>-<version>-<goos>-<goarch>.zip  { <binary>, plugin.json }
//
// The manifest is generated FROM the freshly built binary via `--dump-manifest`,
// so plugin.json can never drift from the binary it describes.
func pluginBuildCmd() *cobra.Command {
	var (
		kind      string
		target    string
		goos      string
		goarch    string
		buildAll  bool
		allInd    bool
		changed   bool
		since     string
		signKey   string
		cosignKey string
		output    string
	)
	cmd := &cobra.Command{
		Use:   "build [name...]",
		Short: "Build plugin(s) of a kind into release zips",
		Long: `Compile one or more plugins (<kind>/<name>/main.go) and pack each built
binary plus its generated plugin.json into a zip named
<name>-<version>-<goos>-<goarch>.zip under bin/.

Plugin KIND (selects the source folder):
  --kind connector   (default) → builds from connector/<name>/
  --kind tool|job              → builds from tool/<name>/ or job/<name>/

Selecting WHICH plugins:
  wick plugin build gmail slack        explicit names
  wick plugin build --all-plugins      every folder under <kind>/
  wick plugin build --changed [--since <ref>]   only folders touched since <ref>
                                                 (default ref: origin/main)

Selecting WHICH targets:
  --target <os>/<arch>   single target (default: host)
  --goos / --goarch      single target, split form
  --all                  every supported os/arch (linux/arm64 first — Termux)

Version per plugin is read from <kind>/<name>/VERSION (falling back to "dev").
Pass --sign-key <path> to sign each manifest (ed25519); generate a key with the
cmd/plugin-keygen tool.`,
		RunE: func(c *cobra.Command, args []string) error {
			if !pluginKinds[kind] {
				return fmt.Errorf("unknown --kind %q (want connector|tool|job)", kind)
			}
			names, err := resolvePluginNames(kind, args, allInd, changed, since)
			if err != nil {
				return err
			}
			if len(names) == 0 {
				return fmt.Errorf("no %s plugins selected (pass names, --all-plugins, or --changed)", kind)
			}
			targets, err := resolvePluginTargets(target, goos, goarch, buildAll)
			if err != nil {
				return err
			}
			if buildAll && output != "" {
				return errors.New("--output is meaningless with --all (zips are named per target)")
			}

			outDir := firstNonEmpty(output, "bin")
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", outDir, err)
			}

			type result struct {
				name, target, zip string
				err               error
			}
			var results []result
			for _, name := range names {
				for _, t := range targets {
					parts := strings.SplitN(t, "/", 2)
					tos, tarch := parts[0], parts[1]
					fmt.Printf("> %s/%s %-15s building...\n", kind, name, t)
					z, err := buildOnePlugin(kind, name, tos, tarch, outDir, signKey, cosignKey)
					results = append(results, result{name, t, z, err})
					if err != nil {
						fmt.Printf("> %s/%s %-15s ✗ %v\n", kind, name, t, err)
						continue
					}
					fmt.Printf("> %s/%s %-15s ✓ %s\n", kind, name, t, z)
				}
			}

			ok := 0
			for _, r := range results {
				if r.err == nil {
					ok++
				}
			}
			fmt.Printf("\nSummary: %d/%d built\n", ok, len(results))
			if ok == 0 {
				return errors.New("every plugin build failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "connector", "Plugin kind: connector|tool|job (selects the <kind>/ source folder)")
	cmd.Flags().StringVarP(&target, "target", "t", "", "Single target <os>/<arch> (e.g. linux/arm64). Mutually exclusive with --goos/--goarch and --all")
	cmd.Flags().StringVar(&goos, "goos", "", "Target GOOS (env: GOOS). Mutually exclusive with --target")
	cmd.Flags().StringVar(&goarch, "goarch", "", "Target GOARCH (env: GOARCH). Mutually exclusive with --target")
	cmd.Flags().BoolVar(&buildAll, "all", false, "Build every supported os/arch (linux/arm64, linux/amd64, darwin/arm64, darwin/amd64, windows/amd64)")
	cmd.Flags().BoolVar(&allInd, "all-plugins", false, "Build every plugin under <kind>/")
	cmd.Flags().BoolVar(&changed, "changed", false, "Build only plugins whose folder changed since --since")
	cmd.Flags().StringVar(&since, "since", "", "Git ref to diff against for --changed (default: origin/main)")
	cmd.Flags().StringVar(&signKey, "sign-key", "", "Path to ed25519 private key; signs each manifest when set")
	cmd.Flags().StringVar(&cosignKey, "cosign-key", "", "Path to a cosign private key; signs each binary with the external cosign CLI (sidecar .sig + .pem in the zip). Soft-skips with a warning if cosign is not on PATH")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output dir for zips (default: bin)")
	return cmd
}

// resolvePluginNames picks the plugin list from the one selector the user
// supplied. Exactly one of {explicit args, --all-plugins, --changed} must be set.
// All paths are scoped to the <kind>/ folder.
func resolvePluginNames(kind string, args []string, allPlugins, changed bool, since string) ([]string, error) {
	selectors := 0
	if len(args) > 0 {
		selectors++
	}
	if allPlugins {
		selectors++
	}
	if changed {
		selectors++
	}
	if selectors > 1 {
		return nil, errors.New("pass exactly one of: plugin names, --all-plugins, --changed")
	}
	switch {
	case len(args) > 0:
		for _, n := range args {
			if _, err := os.Stat(filepath.Join(kind, n, "main.go")); err != nil {
				return nil, fmt.Errorf("%s plugin %q not found (expected %s/%s/main.go)", kind, n, kind, n)
			}
		}
		return args, nil
	case allPlugins:
		return listPluginDirs(kind)
	case changed:
		return changedPluginDirs(kind, firstNonEmpty(since, "origin/main"))
	default:
		return nil, nil
	}
}

// listPluginDirs returns every immediate subdir of <kind>/ that holds a main.go,
// skipping scaffolds (names starting with "_").
func listPluginDirs(kind string) ([]string, error) {
	entries, err := os.ReadDir(kind)
	if err != nil {
		return nil, fmt.Errorf("read %s/: %w", kind, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		if _, err := os.Stat(filepath.Join(kind, e.Name(), "main.go")); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// changedPluginDirs runs `git diff --name-only <ref>...HEAD` and returns the
// distinct <kind>/ folders touched. Falls back to a two-dot diff when the
// three-dot form fails (e.g. shallow clone without the merge base).
func changedPluginDirs(kind, ref string) ([]string, error) {
	out, err := safeexec.Command("git", "diff", "--name-only", ref+"...HEAD").Output()
	if err != nil {
		out, err = safeexec.Command("git", "diff", "--name-only", ref).Output()
		if err != nil {
			return nil, fmt.Errorf("git diff against %s: %w", ref, err)
		}
	}
	seen := map[string]bool{}
	var names []string
	prefix := kind + "/"
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		name := strings.SplitN(strings.TrimPrefix(line, prefix), "/", 2)[0]
		if name == "" || strings.HasPrefix(name, "_") || seen[name] {
			continue
		}
		if _, err := os.Stat(filepath.Join(kind, name, "main.go")); err != nil {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// resolvePluginTargets turns the target flags into the os/arch list to build.
func resolvePluginTargets(target, goos, goarch string, buildAll bool) ([]string, error) {
	if buildAll {
		if target != "" || goos != "" || goarch != "" {
			return nil, errors.New("--all is mutually exclusive with --target/--goos/--goarch")
		}
		return pluginBuildTargets, nil
	}
	if target != "" {
		if goos != "" || goarch != "" {
			return nil, errors.New("--target is mutually exclusive with --goos/--goarch")
		}
		parts := strings.SplitN(target, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, errors.New("--target must be <os>/<arch> (e.g. linux/arm64)")
		}
		return []string{target}, nil
	}
	tos := firstNonEmpty(goos, os.Getenv("GOOS"), runtime.GOOS)
	tarch := firstNonEmpty(goarch, os.Getenv("GOARCH"), runtime.GOARCH)
	return []string{tos + "/" + tarch}, nil
}

// buildOnePlugin compiles <kind>/<name> for one target, generates its manifest
// from the binary, and zips them. When cosignKey is set, the binary is also
// signed with the external cosign CLI and the .sig/.pem sidecars are added to
// the zip. Returns the zip path.
func buildOnePlugin(kind, name, goos, goarch, outDir, signKey, cosignKey string) (string, error) {
	srcDir := filepath.Join(kind, name)
	version := readPluginVersion(kind, name)

	stage, err := os.MkdirTemp("", "wick-plugin-"+name+"-")
	if err != nil {
		return "", fmt.Errorf("mkdir staging: %w", err)
	}
	defer os.RemoveAll(stage)

	binName := name
	if goos == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(stage, binName)

	// -s -w strips the symbol table + DWARF debug info: a connector plugin is
	// shipped, not debugged in place, and this cuts ~30% off the binary (the
	// gRPC/protobuf transport floor is unavoidable, but the debug symbols are
	// pure dead weight in a distributed artifact).
	ldflags := "-s -w -X github.com/yogasw/wick/pkg/plugin.Version=" + version
	build := safeexec.Command("go", "build", "-ldflags", ldflags, "-o", binPath, "./"+filepath.ToSlash(srcDir))
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	build.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch)
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("go build %s: %w", srcDir, err)
	}

	manifestJSON, err := dumpPluginManifest(kind, srcDir, version, signKey, binPath, goos, goarch, binName)
	if err != nil {
		return "", err
	}
	manifestPath := filepath.Join(stage, "plugin.json")
	if err := os.WriteFile(manifestPath, manifestJSON, 0o644); err != nil {
		return "", fmt.Errorf("write plugin.json: %w", err)
	}

	files := map[string]string{
		binName:       binPath,
		"plugin.json": manifestPath,
	}

	// Optional cosign signing of the binary (external CLI; no sigstore Go dep).
	if cosignKey != "" {
		sig, cert, cerr := cosignSignBinary(binPath, cosignKey, stage, binName)
		switch {
		case cerr == errCosignNotFound:
			fmt.Fprintf(os.Stderr, "> %s: cosign not found on PATH — skipping cosign signature (binary still built)\n", name)
		case cerr != nil:
			return "", cerr
		default:
			files[binName+".sig"] = sig
			files[binName+".pem"] = cert
		}
	}

	verSlug := strings.TrimPrefix(strings.TrimSpace(version), "v")
	zipName := fmt.Sprintf("%s-%s-%s-%s.zip", name, verSlug, goos, goarch)
	zipPath := filepath.Join(outDir, zipName)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	if err := zipPluginFiles(zipPath, files); err != nil {
		return "", fmt.Errorf("zip: %w", err)
	}
	return zipPath, nil
}

// readPluginVersion reads <kind>/<name>/VERSION, trimmed; "dev" if absent.
func readPluginVersion(kind, name string) string {
	raw, err := os.ReadFile(filepath.Join(kind, name, "VERSION"))
	if err != nil {
		return "dev"
	}
	v := strings.TrimSpace(string(raw))
	if v == "" {
		return "dev"
	}
	return v
}

// zipPluginFiles writes a zip at dst containing the given arcname->srcpath
// entries, in deterministic name order.
func zipPluginFiles(dst string, files map[string]string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, arc := range names {
		src := files[arc]
		info, err := os.Stat(src)
		if err != nil {
			zw.Close()
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			zw.Close()
			return err
		}
		hdr.Name = arc
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			zw.Close()
			return err
		}
		in, err := os.Open(src)
		if err != nil {
			zw.Close()
			return err
		}
		if _, err := io.Copy(w, in); err != nil {
			in.Close()
			zw.Close()
			return err
		}
		in.Close()
	}
	return zw.Close()
}
