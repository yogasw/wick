package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// Folder is one Team-sidebar folder. Collapsed is the viewer's own fold
// state; the order of Folders.Folders is the order the sidebar shows.
type Folder struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Collapsed bool   `json:"collapsed,omitempty"`
}

// Folders is entity.TeamFolders.Data: a viewer's folders and where each
// agent sits (agent id → folder id). An agent with no entry is loose.
type Folders struct {
	Folders []Folder          `json:"folders"`
	Place   map[string]string `json:"place"`
}

const (
	// MaxFolders caps one viewer's folders.
	MaxFolders = 60
	// MaxFolderName caps a folder name, in runes.
	MaxFolderName = 40
)

var (
	ErrFolderName   = fmt.Errorf("folder name must be 1 to %d characters", MaxFolderName)
	ErrFolderTaken  = errors.New("a folder with that name already exists")
	ErrFolderAbsent = errors.New("no such folder")
)

// DecodeFolders reads Data; "" or broken is empty.
func DecodeFolders(raw string) Folders {
	var f Folders
	_ = json.Unmarshal([]byte(raw), &f)
	return f.normal()
}

// EncodeFolders is the inverse of DecodeFolders.
func EncodeFolders(f Folders) string {
	b, _ := json.Marshal(f.normal())
	return string(b)
}

// normal never returns nil collections, trims names and drops
// placements into a folder that is gone.
func (f Folders) normal() Folders {
	folders := make([]Folder, 0, len(f.Folders))
	ids := map[string]bool{}
	for _, d := range f.Folders {
		d.Name = strings.TrimSpace(d.Name)
		folders = append(folders, d)
		ids[d.ID] = true
	}
	f.Folders = folders
	place := map[string]string{}
	for a, d := range f.Place {
		if a != "" && ids[d] {
			place[a] = d
		}
	}
	f.Place = place
	return f
}

// Validate checks a whole Folders as a client sends it.
func (f Folders) Validate() error {
	if len(f.Folders) > MaxFolders {
		return fmt.Errorf("at most %d folders", MaxFolders)
	}
	seenID, seenName := map[string]bool{}, map[string]bool{}
	for _, d := range f.Folders {
		if d.ID == "" || len(d.ID) > 64 {
			return errors.New("every folder needs an id of at most 64 characters")
		}
		n := strings.TrimSpace(d.Name)
		if n == "" || len([]rune(n)) > MaxFolderName {
			return ErrFolderName
		}
		if seenID[d.ID] {
			return fmt.Errorf("folder id %q is used twice", d.ID)
		}
		if seenName[strings.ToLower(n)] {
			return fmt.Errorf("%w: %q", ErrFolderTaken, n)
		}
		seenID[d.ID], seenName[strings.ToLower(n)] = true, true
	}
	return nil
}

// ByName finds a folder by name, case-insensitively.
func (f Folders) ByName(name string) (Folder, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, d := range f.Folders {
		if strings.ToLower(d.Name) == n {
			return d, true
		}
	}
	return Folder{}, false
}

// NameOf is the name of the folder agentID sits in, "" when loose.
func (f Folders) NameOf(agentID string) string {
	id := f.Place[agentID]
	for _, d := range f.Folders {
		if d.ID == id {
			return d.Name
		}
	}
	return ""
}

// Ensure returns the folder called name, appending it when missing.
func (f *Folders) Ensure(name string) (Folder, error) {
	n := strings.TrimSpace(name)
	if n == "" || len([]rune(n)) > MaxFolderName {
		return Folder{}, ErrFolderName
	}
	if d, ok := f.ByName(n); ok {
		return d, nil
	}
	if len(f.Folders) >= MaxFolders {
		return Folder{}, fmt.Errorf("at most %d folders", MaxFolders)
	}
	d := Folder{ID: uuid.New().String(), Name: n}
	f.Folders = append(f.Folders, d)
	return d, nil
}

// Move puts agentID in the folder called name (made when missing); ""
// takes it out of any folder.
func (f *Folders) Move(agentID, name string) error {
	if f.Place == nil {
		f.Place = map[string]string{}
	}
	if strings.TrimSpace(name) == "" {
		delete(f.Place, agentID)
		return nil
	}
	d, err := f.Ensure(name)
	if err != nil {
		return err
	}
	f.Place[agentID] = d.ID
	return nil
}

// Rename renames the folder called from.
func (f *Folders) Rename(from, to string) error {
	d, ok := f.ByName(from)
	if !ok {
		return ErrFolderAbsent
	}
	n := strings.TrimSpace(to)
	if n == "" || len([]rune(n)) > MaxFolderName {
		return ErrFolderName
	}
	if o, ok := f.ByName(n); ok && o.ID != d.ID {
		return ErrFolderTaken
	}
	for i := range f.Folders {
		if f.Folders[i].ID == d.ID {
			f.Folders[i].Name = n
		}
	}
	return nil
}

// Delete removes the folder called name; its agents become loose.
func (f *Folders) Delete(name string) error {
	d, ok := f.ByName(name)
	if !ok {
		return ErrFolderAbsent
	}
	out := f.Folders[:0]
	for _, x := range f.Folders {
		if x.ID != d.ID {
			out = append(out, x)
		}
	}
	f.Folders = out
	for a, id := range f.Place {
		if id == d.ID {
			delete(f.Place, a)
		}
	}
	return nil
}

// Order puts the named folders first, in that order; the rest keep
// their relative order after them.
func (f *Folders) Order(names []string) error {
	var head []Folder
	used := map[string]bool{}
	for _, n := range names {
		d, ok := f.ByName(n)
		if !ok {
			return fmt.Errorf("%w: %q", ErrFolderAbsent, n)
		}
		if !used[d.ID] {
			head = append(head, d)
			used[d.ID] = true
		}
	}
	for _, d := range f.Folders {
		if !used[d.ID] {
			head = append(head, d)
		}
	}
	f.Folders = head
	return nil
}

// TeamFolders returns userID's folders, empty when they have none.
func (s *Store) TeamFolders(ctx context.Context, userID string) (Folders, error) {
	var row entity.TeamFolders
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DecodeFolders(""), nil
	}
	if err != nil {
		return Folders{}, err
	}
	return DecodeFolders(row.Data), nil
}

// SaveTeamFolders writes userID's whole folders row. It goes through
// UpdateTeamFolders, so it never interleaves with a read-modify-write.
func (s *Store) SaveTeamFolders(ctx context.Context, userID string, f Folders) error {
	_, err := s.UpdateTeamFolders(ctx, userID, func(cur *Folders) error {
		*cur = f
		return nil
	})
	return err
}

// UpdateTeamFolders read-modify-writes userID's folders atomically: fn
// gets the current row and edits it in place, then the result is
// validated and saved. Two callers (parallel Captain moves, a sidebar
// PUT) can no longer each read the old row and drop the other's change.
// One transaction holds the row lock (SELECT … FOR UPDATE on Postgres,
// after inserting an empty row so there is one to lock); a per-user
// mutex covers this process, and sqlite, which has no row locks. An
// error from fn or Validate aborts without writing. Returns the saved
// folders.
func (s *Store) UpdateTeamFolders(ctx context.Context, userID string, fn func(*Folders) error) (Folders, error) {
	if userID == "" {
		return Folders{}, errors.New("team folders: no user")
	}
	mu, _ := s.foldersMu.LoadOrStore(userID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	var saved Folders
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		empty := entity.TeamFolders{UserID: userID, Data: EncodeFolders(Folders{}), UpdatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&empty).Error; err != nil {
			return err
		}
		q := tx
		if tx.Dialector != nil && tx.Dialector.Name() == "postgres" {
			q = tx.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var row entity.TeamFolders
		if err := q.Where("user_id = ?", userID).First(&row).Error; err != nil {
			return err
		}
		f := DecodeFolders(row.Data)
		if err := fn(&f); err != nil {
			return err
		}
		if err := f.Validate(); err != nil {
			return err
		}
		saved = DecodeFolders(EncodeFolders(f))
		return tx.Model(&entity.TeamFolders{}).Where("user_id = ?", userID).
			Updates(map[string]any{"data": EncodeFolders(saved), "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		return Folders{}, err
	}
	return saved, nil
}
