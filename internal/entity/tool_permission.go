package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	pkgentity "github.com/yogasw/wick/pkg/entity"
)

// ToolVisibility is re-exported from pkg/entity so existing callers
// continue to work after the public contract moved out of internal.
type ToolVisibility = pkgentity.ToolVisibility

const (
	VisibilityPublic  = pkgentity.VisibilityPublic
	VisibilityPrivate = pkgentity.VisibilityPrivate
)

// ToolPermission stores the per-tool visibility override set by an admin.
// If no row exists for a tool path the code falls back to the tool's
// declared default visibility.
type ToolPermission struct {
	ToolPath   string         `gorm:"primaryKey;type:varchar(255)"`
	Visibility ToolVisibility `gorm:"type:varchar(50);default:'private'"`
	// Disabled hides the tool from every user (including admins) and makes
	// direct hits to /tools/{slug}/* return 404. Admins re-enable from
	// /admin/tools.
	Disabled  bool `gorm:"default:false"`
	UpdatedAt time.Time
}

// Tag is a first-class label that can be attached to users and tools.
// Renaming a Tag propagates automatically because associations store
// TagID, not the name.
//
// A Tag has two orthogonal-but-combinable purposes:
//   - Access filter: when IsFilter is true and the tag is attached to a
//     Private tool, only users who carry the same tag may access it.
//     Tool-tags without IsFilter are purely cosmetic for access (they
//     don't restrict who can enter).
//   - Group on home: when IsGroup is true, tools carrying the tag are
//     rendered together on the home page under Name. A tool with
//     multiple group tags appears in each group.
//
// A tag can set any combination of IsGroup and IsFilter independently.
type Tag struct {
	ID          string `gorm:"type:varchar(36);primaryKey"`
	Name        string `gorm:"uniqueIndex;type:varchar(100);not null"`
	Description string `gorm:"type:varchar(500)"`
	IsGroup     bool   `gorm:"default:false"`
	IsFilter    bool   `gorm:"default:false"`
	SortOrder   int    `gorm:"default:0"`
	CreatedAt   time.Time
}

func (t *Tag) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	return nil
}

// ToolTag links a tool to a Tag. When a tool is Private and at least one
// ToolTag exists, only users carrying one of those tags may access it.
type ToolTag struct {
	ToolPath string `gorm:"primaryKey;type:varchar(255)"`
	TagID    string `gorm:"primaryKey;type:varchar(36);index"`
}

// UserTag assigns a Tag to a user. Only approved users may carry tags.
type UserTag struct {
	UserID string `gorm:"primaryKey;type:uuid"`
	TagID  string `gorm:"primaryKey;type:varchar(36);index"`
}
