package userconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Provider instances and their probe statuses live in their own files,
// one per instance, under <data dir>/providers (next to model-state):
//
//	providers/configs/<type>-<name>.json   the instance's settings
//	providers/status/<type>-<name>.json    its last probe result
//
// Every read-modify-write of them holds providers/configs/.lock (flock,
// so the old and the new process of a graceful reload take turns too);
// a writer that finds it held waits a moment and tries again.
//
// config.json is shared by every writer of the app (the updater, the
// System page, the tray, the daemon, wick-manager). Each of them loads the
// whole file, changes its own field and writes the whole file back, so a
// copy loaded before a provider edit wrote the old providers back: env
// rows, extra args and model lists "vanished". The updater even keeps the
// copy it loaded at boot. With the providers in files of their own, only
// the provider store writes them, one instance per file.
//
// config.json still carries a mirror of the providers, refreshed from the
// files on every Save, so an older binary on the same data dir (a release
// still draining after a reload, a downgrade) reads what is current.
// The files are the truth: Load reads them, and a Save never writes a
// caller's providers back over them.
//
// Migration: a data dir that has no providers/configs yet takes its
// instances from config.json the first time anything is saved (or loaded
// by a binary that knows this layout), so a device still on an older
// version moves over as soon as it upgrades.

// providerFileMu serialises this process's reads and writes of the
// provider files and the migration; lockProviders adds the flock that
// makes another process (the other half of a reload) take turns too.
var providerFileMu sync.Mutex

// lockProviders takes both, waiting with a short pause between tries
// while another process holds the flock (see lockFile). The returned
// func releases them.
func lockProviders(name string) (func(), error) {
	providerFileMu.Lock()
	root, err := instancesDir(name)
	if err == nil {
		err = os.MkdirAll(root, 0o700)
	}
	if err != nil {
		providerFileMu.Unlock()
		return nil, err
	}
	unlock, err := lockFile(filepath.Join(root, ".lock"))
	if err != nil {
		providerFileMu.Unlock()
		return nil, err
	}
	return func() { unlock(); providerFileMu.Unlock() }, nil
}

// instanceFile is one instance on disk; Position keeps the order the
// Providers page lists them in.
type instanceFile struct {
	Position int              `json:"position"`
	Instance ProviderInstance `json:"instance"`
}

// providerTypes are the instance lists of ProvidersConfig by type key.
func providerTypes(pc *ProvidersConfig) map[string]*[]ProviderInstance {
	return map[string]*[]ProviderInstance{
		"claude":   &pc.Claude,
		"codex":    &pc.Codex,
		"gemini":   &pc.Gemini,
		"omp":      &pc.OMP,
		"opencode": &pc.Opencode,
		"wick":     &pc.Wick,
	}
}

func providersDir(name string) (string, error) {
	dir, err := Dir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "providers"), nil
}

func instancesDir(name string) (string, error) {
	d, err := providersDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "configs"), nil
}

func statusDir(name string) (string, error) {
	d, err := providersDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "status"), nil
}

// migratedMarker is written last by the migration: its presence says
// the files are the source of truth.
const migratedMarker = ".migrated"

func providersMigrated(name string) bool {
	d, err := instancesDir(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(d, migratedMarker))
	return err == nil
}

// migrateProvidersLocked moves the instances and statuses config.json
// still holds into their files. A no-op once migrated. Caller holds
// providerFileMu.
func migrateProvidersLocked(name string) error {
	if providersMigrated(name) {
		return nil
	}
	disk, err := loadFile(name)
	if err != nil {
		return err
	}
	if err := writeInstancesLocked(name, disk.Providers); err != nil {
		return err
	}
	if err := writeStatusesLocked(name, disk.ProviderStatuses); err != nil {
		return err
	}
	d, err := instancesDir(name)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(d, migratedMarker), []byte("providers moved out of config.json\n"))
}

// readInstancesLocked fills pc's instance lists from the files, ordered
// by Position then name. ManagedBinaries is left alone.
func readInstancesLocked(name string, pc *ProvidersConfig) error {
	root, err := instancesDir(name)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	byType := map[string][]instanceFile{}
	for _, e := range entries {
		typ, _, ok := splitInstanceFile(e)
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			return err
		}
		var f instanceFile
		if err := json.Unmarshal(data, &f); err != nil {
			return err
		}
		byType[typ] = append(byType[typ], f)
	}
	for typ, list := range providerTypes(pc) {
		files := byType[typ]
		sort.SliceStable(files, func(i, j int) bool {
			if files[i].Position != files[j].Position {
				return files[i].Position < files[j].Position
			}
			return files[i].Instance.Name < files[j].Instance.Name
		})
		var out []ProviderInstance
		for _, f := range files {
			out = append(out, f.Instance)
		}
		*list = out
	}
	return nil
}

// splitInstanceFile reads "<type>-<name>.json": a known type, then the
// instance name (names hold only letters, digits and "_").
func splitInstanceFile(e os.DirEntry) (typ, inst string, ok bool) {
	if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
		return "", "", false
	}
	typ, inst, ok = strings.Cut(strings.TrimSuffix(e.Name(), ".json"), "-")
	if !ok || inst == "" {
		return "", "", false
	}
	if _, known := providerTypes(&ProvidersConfig{})[typ]; !known {
		return "", "", false
	}
	return typ, inst, true
}

// validFileName reports whether an instance name can name a file.
func validFileName(n string) bool {
	if n == "" {
		return false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// writeInstancesLocked makes the files match pc: an instance whose file
// already holds the same bytes is not rewritten, a file whose instance
// is gone is removed.
func writeInstancesLocked(name string, pc ProvidersConfig) error {
	root, err := instancesDir(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	keep := map[string]bool{}
	for typ, list := range providerTypes(&pc) {
		for i, ins := range *list {
			if !validFileName(ins.Name) {
				continue
			}
			file := typ + "-" + ins.Name + ".json"
			keep[file] = true
			data, err := json.MarshalIndent(instanceFile{Position: i, Instance: ins}, "", "  ")
			if err != nil {
				return err
			}
			if err := writeIfChanged(filepath.Join(root, file), data); err != nil {
				return err
			}
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, _, ok := splitInstanceFile(e); ok && !keep[e.Name()] {
			if err := os.Remove(filepath.Join(root, e.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

// statusFile maps a "<type>/<name>" status key to its file name.
func statusFile(key string) string { return strings.Replace(key, "/", "-", 1) + ".json" }

// statusKey is statusFile reversed; ok=false for any other file.
func statusKey(e os.DirEntry) (string, bool) {
	typ, inst, ok := splitInstanceFile(e)
	if !ok {
		return "", false
	}
	return typ + "/" + inst, true
}

func readStatusesLocked(name string) (map[string]ProviderStatus, error) {
	dir, err := statusDir(name)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := map[string]ProviderStatus{}
	for _, e := range entries {
		key, ok := statusKey(e)
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var ps ProviderStatus
		if err := json.Unmarshal(data, &ps); err != nil {
			return nil, err
		}
		out[key] = ps
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func writeStatusesLocked(name string, st map[string]ProviderStatus) error {
	dir, err := statusDir(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	keep := map[string]bool{}
	for key, ps := range st {
		typ, inst, ok := strings.Cut(key, "/")
		if _, known := providerTypes(&ProvidersConfig{})[typ]; !ok || !known || !validFileName(inst) {
			continue
		}
		file := statusFile(key)
		keep[file] = true
		data, err := json.MarshalIndent(ps, "", "  ")
		if err != nil {
			return err
		}
		if err := writeIfChanged(filepath.Join(dir, file), data); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, ok := statusKey(e); ok && !keep[e.Name()] {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

// overlayProvidersLocked replaces cfg's instances and statuses with what
// the files hold, migrating first when they do not exist yet.
func overlayProvidersLocked(name string, cfg *Config) error {
	if err := migrateProvidersLocked(name); err != nil {
		return err
	}
	if err := readInstancesLocked(name, &cfg.Providers); err != nil {
		return err
	}
	st, err := readStatusesLocked(name)
	if err != nil {
		return err
	}
	cfg.ProviderStatuses = st
	return nil
}

// SaveProviders persists the provider instances of cfg (each to its own
// file; ManagedBinaries and every other setting stay in config.json) and
// refreshes config.json's mirror. Only the provider store calls it.
func SaveProviders(name string, pc ProvidersConfig) error {
	unlock, err := lockProviders(name)
	if err != nil {
		return err
	}
	defer unlock()
	if err := migrateProvidersLocked(name); err != nil {
		return err
	}
	if err := writeInstancesLocked(name, pc); err != nil {
		return err
	}
	return refreshMirrorLocked(name)
}

// SaveProviderStatuses persists the cached probe statuses (one file per
// instance) and refreshes config.json's mirror.
func SaveProviderStatuses(name string, st map[string]ProviderStatus) error {
	unlock, err := lockProviders(name)
	if err != nil {
		return err
	}
	defer unlock()
	if err := migrateProvidersLocked(name); err != nil {
		return err
	}
	if err := writeStatusesLocked(name, st); err != nil {
		return err
	}
	return refreshMirrorLocked(name)
}

// refreshMirrorLocked rewrites config.json with its other settings as
// they are on disk and the providers as the files hold them. Caller
// holds lockProviders.
func refreshMirrorLocked(name string) error {
	cfg, err := loadFile(name)
	if err != nil {
		return err
	}
	if err := overlayProvidersLocked(name, &cfg); err != nil {
		return err
	}
	return saveFile(name, cfg)
}

// writeIfChanged writes data to path atomically unless the file already
// holds exactly data.
func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeAtomic(path, data)
}

// writeAtomic writes data to a unique temp file next to path, then
// renames it over path.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
