// Package replace moves the stored data of a replaced job/tool key onto the
// module that declared `Replaces` — typically a plugin extracted from a
// built-in, whose key differs (notion-ticket-sync → notion_ticket_sync).
//
// What moves, once per (old, new) pair:
//   - config rows whose key the new module declares, copied as STORED (a
//     secret's ciphertext is copied byte for byte, never decrypted). A value
//     already set on the new key is kept; only an empty field, or a
//     non-secret still equal to the plugin's declared default, is filled.
//   - jobs: schedule, enabled, max_runs and max_timeout_min, while the new
//     job row is still pristine (never configured). Run history stays on the
//     old job row; the report says how many runs it holds.
//   - tags, the visibility override and bookmarks of /jobs/<old> or
//     /tools/<old>, merged into the new path.
//
// After that the old key is hidden: the caller unregisters it (Prepare), the
// jobs service filters it out of every listing (IsReplaced) and its job row is
// disabled on every boot, so the old job can never run next to the new one.
// The old rows are not deleted, so removing the plugin brings the old item
// back intact.
package replace

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
	pkgentity "github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
)

// Item kinds a pair can carry.
const (
	KindJob  = "job"
	KindTool = "tool"
)

// Field actions in a Report.
const (
	ActionCopy = "copy"
	ActionKeep = "keep"
	ActionSkip = "skip"
)

// Pair is one declared replacement: the module New took over Old.
type Pair struct {
	Kind string `json:"kind"`
	Old  string `json:"old"`
	New  string `json:"new"`
	// Configs are the rows the new module declares (Owner unset).
	Configs []pkgentity.Config `json:"-"`
	// DefaultCron is the new job's declared schedule, used to tell a
	// pristine job row from one an admin already configured.
	DefaultCron string `json:"-"`
}

// Path is the item path tags, visibility and bookmarks hang off.
func Path(kind, key string) string {
	if kind == KindJob {
		return "/jobs/" + key
	}
	return "/tools/" + key
}

var active = struct {
	sync.RWMutex
	byOld map[string]Pair
}{byOld: map[string]Pair{}}

// Collect reads Replaces off every registered job and tool module. Self
// references, blanks and an old key claimed twice (first wins) are dropped
// with a warning.
func Collect(jobMods []job.Module, toolMods []tool.Module) []Pair {
	var pairs []Pair
	seen := map[string]string{}
	add := func(p Pair) {
		p.Old = strings.TrimSpace(p.Old)
		if p.Old == "" || p.Old == p.New {
			return
		}
		if by, dup := seen[p.Kind+"/"+p.Old]; dup {
			log.Warn().Str("old", p.Old).Str("new", p.New).Str("claimed_by", by).Msg("replace: old key already claimed, ignored")
			return
		}
		seen[p.Kind+"/"+p.Old] = p.New
		pairs = append(pairs, p)
	}
	for _, m := range jobMods {
		for _, old := range m.Meta.Replaces {
			add(Pair{Kind: KindJob, Old: old, New: m.Meta.Key, Configs: m.Configs, DefaultCron: m.Meta.DefaultCron})
		}
	}
	for _, m := range toolMods {
		for _, old := range m.Meta.Replaces {
			add(Pair{Kind: KindTool, Old: old, New: m.Meta.Key, Configs: m.Configs})
		}
	}
	return pairs
}

// Prepare collects the declared pairs, unregisters every replaced key so the
// old module is neither bootstrapped nor routed, and remembers the pairs for
// IsReplaced / Lookup. Call it after every job/tool (built-in and plugin) is
// registered and before validation and the configs/jobs bootstrap.
func Prepare(jobMods []job.Module, toolMods []tool.Module, unregJob, unregTool func(string) bool) []Pair {
	pairs := Collect(jobMods, toolMods)
	active.Lock()
	defer active.Unlock()
	for _, p := range pairs {
		active.byOld[p.Kind+"/"+p.Old] = p
		unreg := unregTool
		if p.Kind == KindJob {
			unreg = unregJob
		}
		if unreg != nil && unreg(p.Old) {
			log.Info().Str("kind", p.Kind).Str("old", p.Old).Str("new", p.New).Msg("replace: old module superseded by plugin")
		}
	}
	return pairs
}

// IsReplaced reports whether a job key was replaced by another module.
func IsReplaced(jobKey string) bool {
	active.RLock()
	defer active.RUnlock()
	_, ok := active.byOld[KindJob+"/"+jobKey]
	return ok
}

// Lookup returns the active pairs whose new key is newKey.
func Lookup(newKey string) []Pair {
	active.RLock()
	defer active.RUnlock()
	var out []Pair
	for _, p := range active.byOld {
		if p.New == newKey {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Old < out[j].Old })
	return out
}

// reset clears the active set (tests).
func reset() {
	active.Lock()
	active.byOld = map[string]Pair{}
	active.Unlock()
}

// FieldReport is what happens to one config key. Values of secret fields are
// shown as "set"/"empty" only.
type FieldReport struct {
	Key    string `json:"key"`
	Secret bool   `json:"secret"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// JobReport is what happens to the job row.
type JobReport struct {
	Schedule      string `json:"schedule"`
	Enabled       bool   `json:"enabled"`
	MaxRuns       int    `json:"max_runs"`
	MaxTimeoutMin int    `json:"max_timeout_min"`
	Action        string `json:"action"`
	Reason        string `json:"reason"`
	// OldDisabled is true when the old row was enabled and gets switched off.
	OldDisabled bool `json:"old_disabled"`
	// HistoryRuns is how many runs stay recorded under the old key.
	HistoryRuns int64 `json:"history_runs"`
}

// AccessReport is what happens to tags, visibility and bookmarks.
type AccessReport struct {
	TagsAdded      int    `json:"tags_added"`
	Visibility     string `json:"visibility"` // copy / keep / skip
	BookmarksAdded int    `json:"bookmarks_added"`
}

// Report describes one pair's migration (planned when DryRun).
type Report struct {
	Kind   string `json:"kind"`
	Old    string `json:"old"`
	New    string `json:"new"`
	DryRun bool   `json:"dry_run"`
	// AlreadyDone: the pair was migrated before and no force was asked, so
	// nothing moved this time (the old job is still kept disabled).
	AlreadyDone bool          `json:"already_done"`
	MigratedAt  *time.Time    `json:"migrated_at,omitempty"`
	Configs     []FieldReport `json:"configs"`
	Job         *JobReport    `json:"job,omitempty"`
	Access      AccessReport  `json:"access"`
}

// Summary is a one-line, value-free description for logs and audit.
func (r Report) Summary() string {
	copied := []string{}
	for _, f := range r.Configs {
		if f.Action == ActionCopy {
			copied = append(copied, f.Key)
		}
	}
	s := fmt.Sprintf("%s %s -> %s: configs copied [%s]", r.Kind, r.Old, r.New, strings.Join(copied, ","))
	if r.Job != nil {
		s += fmt.Sprintf("; job %s (schedule=%q enabled=%t), old disabled=%t, %d runs stay on %s",
			r.Job.Action, r.Job.Schedule, r.Job.Enabled, r.Job.OldDisabled, r.Job.HistoryRuns, r.Old)
	}
	s += fmt.Sprintf("; tags +%d, visibility %s, bookmarks +%d", r.Access.TagsAdded, r.Access.Visibility, r.Access.BookmarksAdded)
	return s
}

// Migrator runs replacements against the database.
type Migrator struct {
	db *gorm.DB
}

// New returns a Migrator over db.
func New(db *gorm.DB) *Migrator { return &Migrator{db: db} }

// Plan reports what Apply would move, without writing anything.
func (m *Migrator) Plan(ctx context.Context, p Pair) (Report, error) {
	var rep Report
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		rep, err = run(tx, p, false, true)
		return err
	})
	return rep, err
}

// Apply migrates p. A pair already migrated is skipped unless force; either
// way the old job row is kept disabled. actor is recorded in the audit row.
func (m *Migrator) Apply(ctx context.Context, p Pair, actor string, force bool) (Report, error) {
	var rep Report
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		rep, err = run(tx, p, true, force)
		if err != nil || rep.AlreadyDone {
			return err
		}
		now := time.Now()
		rep.MigratedAt = &now
		marker := entity.PluginReplacement{OldKey: p.Old, NewKey: p.New, Kind: p.Kind, MigratedAt: now, Detail: rep.Summary()}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "old_key"}, {Name: "new_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"kind", "migrated_at", "detail"}),
		}).Create(&marker).Error; err != nil {
			return fmt.Errorf("record replacement: %w", err)
		}
		return tx.Create(&entity.PluginAudit{At: now, Actor: actor, Action: "replace.migrate", Key: p.New, Detail: rep.Summary()}).Error
	})
	return rep, err
}

// ApplyAll applies every pair at boot, logging (never failing) per pair.
func (m *Migrator) ApplyAll(ctx context.Context, pairs []Pair) {
	for _, p := range pairs {
		rep, err := m.Apply(ctx, p, "system", false)
		if err != nil {
			log.Warn().Err(err).Str("old", p.Old).Str("new", p.New).Msg("replace: migration failed")
			continue
		}
		if rep.AlreadyDone {
			continue
		}
		log.Info().Str("old", p.Old).Str("new", p.New).Msg("replace: migrated — " + rep.Summary())
	}
}

func run(tx *gorm.DB, p Pair, write, force bool) (Report, error) {
	rep := Report{Kind: p.Kind, Old: p.Old, New: p.New, DryRun: !write, Configs: []FieldReport{}}
	var marker entity.PluginReplacement
	err := tx.Where(map[string]any{"old_key": p.Old, "new_key": p.New}).Take(&marker).Error
	switch {
	case err == nil:
		at := marker.MigratedAt
		rep.MigratedAt = &at
		if !force {
			rep.AlreadyDone = true
		}
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return rep, fmt.Errorf("load replacement marker: %w", err)
	}
	if p.Kind == KindJob {
		jr, err := migrateJob(tx, p, write, !rep.AlreadyDone)
		if err != nil {
			return rep, err
		}
		rep.Job = jr
	}
	if rep.AlreadyDone {
		return rep, nil
	}
	if rep.Configs, err = migrateConfigs(tx, p, write); err != nil {
		return rep, err
	}
	if rep.Access, err = migrateAccess(tx, Path(p.Kind, p.Old), Path(p.Kind, p.New), write); err != nil {
		return rep, err
	}
	return rep, nil
}

func findConfig(tx *gorm.DB, owner, key string) (*pkgentity.Config, error) {
	var c pkgentity.Config
	err := tx.Where(map[string]any{"owner": owner, "key": key}).Take(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load config %s/%s: %w", owner, key, err)
	}
	return &c, nil
}

func shown(secret bool, v string) string {
	if !secret {
		return v
	}
	if v == "" {
		return "empty"
	}
	return "set"
}

func migrateConfigs(tx *gorm.DB, p Pair, write bool) ([]FieldReport, error) {
	out := []FieldReport{}
	declared := map[string]bool{}
	for _, d := range p.Configs {
		declared[d.Key] = true
		old, err := findConfig(tx, p.Old, d.Key)
		if err != nil {
			return nil, err
		}
		cur, err := findConfig(tx, p.New, d.Key)
		if err != nil {
			return nil, err
		}
		f := FieldReport{Key: d.Key, Secret: d.IsSecret}
		curVal := ""
		if cur != nil {
			curVal = cur.Value
		}
		f.New = shown(d.IsSecret, curVal)
		if old == nil || old.Value == "" {
			f.Old, f.Action, f.Reason = shown(d.IsSecret, ""), ActionSkip, "no value on "+p.Old
			out = append(out, f)
			continue
		}
		f.Old = shown(d.IsSecret, old.Value)
		switch {
		case old.IsSecret != d.IsSecret:
			// A ciphertext must never land in a plain field (or the reverse).
			f.Action, f.Reason = ActionSkip, "secret flag differs between the two keys"
		case cur == nil || cur.Value == "":
			f.Action, f.Reason = ActionCopy, "empty on "+p.New
		case cur.Value == old.Value:
			f.Action, f.Reason = ActionKeep, "already equal"
		case d.IsSecret:
			f.Action, f.Reason = ActionKeep, "already set on "+p.New
		case cur.Value == d.Value:
			f.Action, f.Reason = ActionCopy, "still at the plugin default"
		default:
			f.Action, f.Reason = ActionKeep, "set manually on "+p.New
		}
		if write && f.Action == ActionCopy {
			if cur == nil {
				row := d
				row.Owner, row.Value = p.New, old.Value
				if err := tx.Create(&row).Error; err != nil {
					return nil, fmt.Errorf("create config %s/%s: %w", p.New, d.Key, err)
				}
			} else if err := tx.Model(&pkgentity.Config{}).Where(map[string]any{"owner": p.New, "key": d.Key}).
				Update("value", old.Value).Error; err != nil {
				return nil, fmt.Errorf("update config %s/%s: %w", p.New, d.Key, err)
			}
		}
		out = append(out, f)
	}
	// Old fields the new module does not declare are listed, never moved.
	var olds []pkgentity.Config
	if err := tx.Where(map[string]any{"owner": p.Old}).Order("key").Find(&olds).Error; err != nil {
		return nil, fmt.Errorf("list configs of %s: %w", p.Old, err)
	}
	for _, o := range olds {
		if declared[o.Key] || o.Value == "" {
			continue
		}
		out = append(out, FieldReport{Key: o.Key, Secret: o.IsSecret, Old: shown(o.IsSecret, o.Value), New: shown(o.IsSecret, ""),
			Action: ActionSkip, Reason: "not declared by " + p.New})
	}
	return out, nil
}

func findJob(tx *gorm.DB, key string) (*entity.Job, error) {
	var j entity.Job
	err := tx.Where(map[string]any{"key": key}).Take(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load job %s: %w", key, err)
	}
	return &j, nil
}

// migrateJob always keeps the old row disabled; settings move only when
// moveSettings (first migration or forced) and the new row is pristine.
func migrateJob(tx *gorm.DB, p Pair, write, moveSettings bool) (*JobReport, error) {
	old, err := findJob(tx, p.Old)
	if err != nil || old == nil {
		return nil, err
	}
	jr := &JobReport{Schedule: old.Schedule, Enabled: old.Enabled, MaxRuns: old.MaxRuns, MaxTimeoutMin: old.MaxTimeoutMin, Action: ActionSkip, Reason: "already migrated"}
	if err := tx.Model(&entity.JobRun{}).Where(map[string]any{"job_id": old.ID}).Count(&jr.HistoryRuns).Error; err != nil {
		return nil, fmt.Errorf("count runs of %s: %w", p.Old, err)
	}
	if old.Enabled {
		jr.OldDisabled = true
		if write {
			if err := tx.Model(&entity.Job{}).Where(map[string]any{"id": old.ID}).Update("enabled", false).Error; err != nil {
				return nil, fmt.Errorf("disable job %s: %w", p.Old, err)
			}
		}
	}
	if !moveSettings {
		return jr, nil
	}
	cur, err := findJob(tx, p.New)
	if err != nil {
		return nil, err
	}
	settings := map[string]any{"schedule": old.Schedule, "enabled": old.Enabled, "max_runs": old.MaxRuns, "max_timeout_min": old.MaxTimeoutMin}
	switch {
	case cur == nil:
		jr.Action, jr.Reason = ActionCopy, "no job row for "+p.New+" yet"
		if write {
			// Bootstrap fills name/description/icon and keeps these fields.
			row := entity.Job{Key: p.New, Name: p.New, Schedule: old.Schedule, Enabled: old.Enabled, MaxRuns: old.MaxRuns, MaxTimeoutMin: old.MaxTimeoutMin}
			if err := tx.Create(&row).Error; err != nil {
				return nil, fmt.Errorf("create job %s: %w", p.New, err)
			}
			// gorm skips zero values on Create, so false/0 fall back to the
			// column defaults — write them explicitly.
			if err := tx.Model(&entity.Job{}).Where(map[string]any{"id": row.ID}).Updates(settings).Error; err != nil {
				return nil, fmt.Errorf("set job %s: %w", p.New, err)
			}
		}
	case !cur.Enabled && cur.Schedule == p.DefaultCron && cur.MaxRuns == 0 && cur.TotalRuns == 0:
		jr.Action, jr.Reason = ActionCopy, p.New+" never configured"
		if write {
			if err := tx.Model(&entity.Job{}).Where(map[string]any{"id": cur.ID}).Updates(settings).Error; err != nil {
				return nil, fmt.Errorf("set job %s: %w", p.New, err)
			}
		}
	default:
		jr.Action, jr.Reason = ActionKeep, p.New+" already configured or has run"
	}
	return jr, nil
}

func migrateAccess(tx *gorm.DB, oldPath, newPath string, write bool) (AccessReport, error) {
	ar := AccessReport{Visibility: ActionSkip}
	var oldTags, newTags []entity.ToolTag
	if err := tx.Where(map[string]any{"tool_path": oldPath}).Find(&oldTags).Error; err != nil {
		return ar, fmt.Errorf("list tags of %s: %w", oldPath, err)
	}
	if err := tx.Where(map[string]any{"tool_path": newPath}).Find(&newTags).Error; err != nil {
		return ar, fmt.Errorf("list tags of %s: %w", newPath, err)
	}
	has := map[string]bool{}
	for _, t := range newTags {
		has[t.TagID] = true
	}
	for _, t := range oldTags {
		if has[t.TagID] {
			continue
		}
		ar.TagsAdded++
		if write {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entity.ToolTag{ToolPath: newPath, TagID: t.TagID}).Error; err != nil {
				return ar, fmt.Errorf("add tag to %s: %w", newPath, err)
			}
		}
	}

	var oldPerm, newPerm entity.ToolPermission
	errOld := tx.Where(map[string]any{"tool_path": oldPath}).Take(&oldPerm).Error
	errNew := tx.Where(map[string]any{"tool_path": newPath}).Take(&newPerm).Error
	for _, e := range []error{errOld, errNew} {
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return ar, fmt.Errorf("load visibility: %w", e)
		}
	}
	switch {
	case errOld != nil:
		ar.Visibility = ActionSkip
	case errNew == nil:
		ar.Visibility = ActionKeep
	default:
		ar.Visibility = ActionCopy
		if write {
			perm := entity.ToolPermission{ToolPath: newPath, Visibility: oldPerm.Visibility}
			if err := tx.Create(&perm).Error; err != nil {
				return ar, fmt.Errorf("copy visibility to %s: %w", newPath, err)
			}
			if oldPerm.Disabled {
				if err := tx.Model(&entity.ToolPermission{}).Where(map[string]any{"tool_path": newPath}).Update("disabled", true).Error; err != nil {
					return ar, fmt.Errorf("copy disabled to %s: %w", newPath, err)
				}
			}
		}
	}

	var oldMarks []entity.Bookmark
	if err := tx.Where(map[string]any{"tool_path": oldPath}).Find(&oldMarks).Error; err != nil {
		return ar, fmt.Errorf("list bookmarks of %s: %w", oldPath, err)
	}
	for _, b := range oldMarks {
		var n int64
		if err := tx.Model(&entity.Bookmark{}).Where(map[string]any{"user_id": b.UserID, "tool_path": newPath}).Count(&n).Error; err != nil {
			return ar, fmt.Errorf("check bookmark: %w", err)
		}
		if n > 0 {
			continue
		}
		ar.BookmarksAdded++
		if write {
			if err := tx.Create(&entity.Bookmark{UserID: b.UserID, ToolPath: newPath}).Error; err != nil {
				return ar, fmt.Errorf("copy bookmark: %w", err)
			}
		}
	}
	return ar, nil
}
