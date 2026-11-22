package connectors

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// Connector TYPE sharing — who may know a connector exists at all.
//
// Tags scope INSTANCES (tool path "/connectors/<id>"). This file adds the
// level above them: the connector type, tool path "/connector-types/<key>".
// It reuses the tag tables as-is, so there is no new access system:
//
//   - no filter tag on the type path        → everyone (today's behaviour,
//     and the default for built-ins and installed plugins)
//   - one or more filter tags on the path   → only holders of one of those
//     tags, the type's owner (creator of a custom connector) and admins
//
// The owner shares by email: the user is added as a holder of the type's
// own "share:<key>" tag. Admins may additionally link any existing filter
// tag (a team tag) or open the type to everyone.
//
// A hidden type drops out of the manager catalogue, wick_list/wick_search
// and dispatch for everyone it is not shared with. Instances a user
// CREATED keep working for that user — hiding a type stops new use, it
// does not pull a working connector from under its owner.

// TypeSharePathPrefix prefixes the tool path of a connector type.
const TypeSharePathPrefix = "/connector-types/"

// ShareTagPrefix prefixes the per-type sharing tag ("share:<key>").
const ShareTagPrefix = "share:"

// TypeSharePath is the tool path a connector type's sharing tags link to.
func TypeSharePath(key string) string { return TypeSharePathPrefix + key }

// ShareTagName is the per-type tag whose holders a connector is shared with.
func ShareTagName(key string) string { return ShareTagPrefix + key }

// ErrTypeOpen is returned when sharing with a person would be a no-op
// because the connector type is already open to everyone.
var ErrTypeOpen = errors.New("connector is visible to everyone; restrict it before sharing with specific people")

// TypeSharing describes who can see one connector type.
type TypeSharing struct {
	// Restricted is false when the type is open to everyone.
	Restricted bool
	// Owner is the type's creator (custom connectors only), "" otherwise.
	Owner string
	// UserIDs hold the type's own share tag (people it was shared with).
	UserIDs []string
	// Tags are the other filter tags an admin linked to the type.
	Tags []entity.Tag
}

// SetTypeOwnerResolver wires the lookup from connector key to its creator
// user id (custom connectors). Built-ins and plugins resolve to "".
func (s *Service) SetTypeOwnerResolver(fn func(ctx context.Context, key string) string) {
	s.typeOwner = fn
}

// TypeOwner returns the creator of connector type key ("" for built-ins
// and plugins).
func (s *Service) TypeOwner(ctx context.Context, key string) string { return s.typeOwnerOf(ctx, key) }

func (s *Service) typeOwnerOf(ctx context.Context, key string) string {
	if s.typeOwner == nil {
		return ""
	}
	return s.typeOwner(ctx, key)
}

func (s *Service) typeDB(ctx context.Context) *gorm.DB {
	if s == nil || s.repo == nil || s.repo.db == nil {
		return nil
	}
	return s.repo.db.WithContext(ctx)
}

// restrictedTypes maps every restricted connector key to the filter tag ids
// linked to its type path. One query; keys absent from the map are open.
func (s *Service) restrictedTypes(ctx context.Context) (map[string][]string, error) {
	out := map[string][]string{}
	db := s.typeDB(ctx)
	if db == nil {
		return out, nil
	}
	var rows []entity.ToolTag
	err := db.Table("tool_tags").
		Select("tool_tags.tool_path, tool_tags.tag_id").
		Joins("JOIN tags t ON t.id = tool_tags.tag_id").
		Where("tool_tags.tool_path LIKE ? AND t.is_filter = ?", TypeSharePathPrefix+"%", true).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		key := strings.TrimPrefix(r.ToolPath, TypeSharePathPrefix)
		out[key] = append(out[key], r.TagID)
	}
	return out, nil
}

// heldTags returns which of tagIDs the user carries, read live from
// user_tags (not the session cookie, so a fresh share applies at once).
func (s *Service) heldTags(ctx context.Context, userID string, tagIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	db := s.typeDB(ctx)
	if db == nil || userID == "" || len(tagIDs) == 0 {
		return out, nil
	}
	var held []string
	if err := db.Model(&entity.UserTag{}).
		Where("user_id = ? AND tag_id IN ?", userID, tagIDs).
		Pluck("tag_id", &held).Error; err != nil {
		return nil, err
	}
	for _, id := range held {
		out[id] = true
	}
	return out, nil
}

// typeGate answers "may this caller see connector type <key>?" for a batch
// of keys with at most two queries plus one owner lookup per restricted key.
type typeGate struct {
	restricted map[string][]string
	held       map[string]bool
	owner      map[string]string
	userID     string
	open       bool
}

func (g *typeGate) allows(key string) bool {
	if g == nil || g.open {
		return true
	}
	ids, ok := g.restricted[key]
	if !ok {
		return true
	}
	if g.userID != "" && g.owner[key] == g.userID {
		return true
	}
	for _, id := range ids {
		if g.held[id] {
			return true
		}
	}
	return false
}

// loadTypeGate builds the gate for one caller. Admins always pass. A load
// error opens the gate rather than hiding every connector on a transient
// DB hiccup — instance tags still apply underneath.
func (s *Service) loadTypeGate(ctx context.Context, userID string, isAdmin bool) *typeGate {
	if isAdmin {
		return &typeGate{open: true}
	}
	restricted, err := s.restrictedTypes(ctx)
	if err != nil || len(restricted) == 0 {
		return &typeGate{open: true}
	}
	var all []string
	for _, ids := range restricted {
		all = append(all, ids...)
	}
	held, err := s.heldTags(ctx, userID, all)
	if err != nil {
		return &typeGate{open: true}
	}
	owner := make(map[string]string, len(restricted))
	for key := range restricted {
		owner[key] = s.typeOwnerOf(ctx, key)
	}
	return &typeGate{restricted: restricted, held: held, owner: owner, userID: userID}
}

// TypeVisibleTo reports whether the caller may see connector type key.
func (s *Service) TypeVisibleTo(ctx context.Context, key, userID string, isAdmin bool) bool {
	return s.loadTypeGate(ctx, userID, isAdmin).allows(key)
}

// TypeVisibility returns a gate function for many keys at once (catalogue
// listings). Same rule as TypeVisibleTo.
func (s *Service) TypeVisibility(ctx context.Context, userID string, isAdmin bool) func(key string) bool {
	g := s.loadTypeGate(ctx, userID, isAdmin)
	return g.allows
}

// filterRowsByType drops instance rows whose type the caller may not see,
// keeping rows the caller created (see the file comment).
func (s *Service) filterRowsByType(ctx context.Context, rows []entity.Connector, userID string, isAdmin bool) []entity.Connector {
	g := s.loadTypeGate(ctx, userID, isAdmin)
	if g.open {
		return rows
	}
	out := rows[:0:len(rows)]
	for _, r := range rows {
		if g.allows(r.Key) || OwnsConnector(r, userID) {
			out = append(out, r)
		}
	}
	return out
}

// GetTypeSharing reads who can see connector type key.
func (s *Service) GetTypeSharing(ctx context.Context, key string) (TypeSharing, error) {
	out := TypeSharing{Owner: s.typeOwnerOf(ctx, key)}
	db := s.typeDB(ctx)
	if db == nil {
		return out, nil
	}
	restricted, err := s.restrictedTypes(ctx)
	if err != nil {
		return out, err
	}
	ids := restricted[key]
	out.Restricted = len(ids) > 0
	share, err := s.shareTag(ctx, key, false)
	if err != nil {
		return out, err
	}
	if share != nil {
		if err := db.Model(&entity.UserTag{}).Where("tag_id = ?", share.ID).
			Order("user_id").Pluck("user_id", &out.UserIDs).Error; err != nil {
			return out, err
		}
	}
	var others []string
	for _, id := range ids {
		if share == nil || id != share.ID {
			others = append(others, id)
		}
	}
	if len(others) > 0 {
		if err := db.Where("id IN ?", others).Order("name").Find(&out.Tags).Error; err != nil {
			return out, err
		}
	}
	return out, nil
}

// shareTag returns the type's "share:<key>" tag, creating it when create is
// true. nil, nil when it does not exist and create is false.
func (s *Service) shareTag(ctx context.Context, key string, create bool) (*entity.Tag, error) {
	db := s.typeDB(ctx)
	if db == nil {
		return nil, nil
	}
	name := ShareTagName(key)
	var t entity.Tag
	err := db.Where("name = ?", name).First(&t).Error
	if err == nil {
		return &t, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if !create {
		return nil, nil
	}
	t = entity.Tag{
		Name:        name,
		Description: "People connector '" + key + "' is shared with. Managed from the connector's Share dialog.",
		IsFilter:    true,
		SortOrder:   2100,
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&t).Error; err != nil {
		return nil, err
	}
	if err := db.Where("name = ?", name).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Service) linkTypeTag(ctx context.Context, key, tagID string) error {
	return s.typeDB(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&entity.ToolTag{ToolPath: TypeSharePath(key), TagID: tagID}).Error
}

// RestrictType hides connector type key from everyone it is not shared
// with. ownerID, when set, is added as a holder of the share tag so the
// owner keeps seeing it even if ownership resolution changes later.
func (s *Service) RestrictType(ctx context.Context, key, ownerID string) error {
	if s.typeDB(ctx) == nil {
		return nil
	}
	t, err := s.shareTag(ctx, key, true)
	if err != nil {
		return err
	}
	if err := s.linkTypeTag(ctx, key, t.ID); err != nil {
		return err
	}
	if ownerID != "" {
		return s.addHolder(ctx, t.ID, ownerID)
	}
	return nil
}

// OpenType makes connector type key visible to everyone again. The share
// tag and its holders are kept, so restricting again restores the list.
func (s *Service) OpenType(ctx context.Context, key string) error {
	db := s.typeDB(ctx)
	if db == nil {
		return nil
	}
	return db.Where("tool_path = ?", TypeSharePath(key)).Delete(&entity.ToolTag{}).Error
}

func (s *Service) addHolder(ctx context.Context, tagID, userID string) error {
	return s.typeDB(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&entity.UserTag{UserID: userID, TagID: tagID}).Error
}

// ShareTypeWithUser adds userID to the people connector type key is shared
// with. Fails with ErrTypeOpen when the type is open to everyone.
func (s *Service) ShareTypeWithUser(ctx context.Context, key, userID string) error {
	if s.typeDB(ctx) == nil {
		return nil
	}
	sh, err := s.GetTypeSharing(ctx, key)
	if err != nil {
		return err
	}
	if !sh.Restricted {
		return ErrTypeOpen
	}
	t, err := s.shareTag(ctx, key, true)
	if err != nil {
		return err
	}
	if err := s.linkTypeTag(ctx, key, t.ID); err != nil {
		return err
	}
	return s.addHolder(ctx, t.ID, userID)
}

// UnshareTypeUser removes userID from the people connector type key is
// shared with. Tags an admin linked are not touched.
func (s *Service) UnshareTypeUser(ctx context.Context, key, userID string) error {
	db := s.typeDB(ctx)
	if db == nil {
		return nil
	}
	t, err := s.shareTag(ctx, key, false)
	if err != nil || t == nil {
		return err
	}
	return db.Where("user_id = ? AND tag_id = ?", userID, t.ID).Delete(&entity.UserTag{}).Error
}

// LinkTypeTag shares connector type key with every holder of tagID (admin).
// Only filter tags restrict access, so a non-filter tag is refused.
func (s *Service) LinkTypeTag(ctx context.Context, key, tagID string) error {
	db := s.typeDB(ctx)
	if db == nil {
		return nil
	}
	var t entity.Tag
	if err := db.Where("id = ?", tagID).First(&t).Error; err != nil {
		return err
	}
	if !t.IsFilter {
		return errors.New("tag " + t.Name + " is not a filter tag")
	}
	return s.linkTypeTag(ctx, key, tagID)
}

// UnlinkTypeTag removes an admin-linked tag from connector type key. Removing
// the last filter tag opens the type to everyone, same as instance tags.
func (s *Service) UnlinkTypeTag(ctx context.Context, key, tagID string) error {
	db := s.typeDB(ctx)
	if db == nil {
		return nil
	}
	return db.Where("tool_path = ? AND tag_id = ?", TypeSharePath(key), tagID).Delete(&entity.ToolTag{}).Error
}

// ShareableTags lists the filter tags an admin may link to a connector type:
// every filter tag except per-resource markers (owner:, share:, custom:).
func (s *Service) ShareableTags(ctx context.Context) ([]entity.Tag, error) {
	db := s.typeDB(ctx)
	if db == nil {
		return nil, nil
	}
	var out []entity.Tag
	err := db.Where("is_filter = ? AND name NOT LIKE ? AND name NOT LIKE ? AND name NOT LIKE ?",
		true, "owner:%", ShareTagPrefix+"%", "custom:%").
		Order("name").Find(&out).Error
	return out, err
}
