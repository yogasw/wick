package tags

import (
	"errors"

	"github.com/yogasw/wick/internal/entity"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// OwnerMode says how SetOwnerTx changes the holders of an owner tag.
type OwnerMode int

const (
	// OwnerAdd links the user as a holder and leaves every other holder in
	// place. The automatic owner tags of connectors, projects, workflows,
	// skills and data tables use it; on those surfaces an admin may also
	// grant the owner tag to more people, and those grants are shares.
	OwnerAdd OwnerMode = iota
	// OwnerTransfer unlinks the previous owner (from) and links the new one,
	// leaving everyone else's grant alone. The admin owner pickers of
	// connectors, projects and workflows use it.
	OwnerTransfer
	// OwnerSole makes the user the ONE holder. Provider instances use it: a
	// provider has exactly one owner. Tool-path links are kept as they are,
	// so a tag that makes the instance private to its owner keeps doing so
	// for the new owner.
	OwnerSole
)

// SetOwnerTx is the one writer of "owner:<resourceID>" holders, shared by
// the automatic owner tags (Service) and the admin owner pickers (internal/
// admin). Run it inside the caller's transaction.
//
// It creates the tag when missing. toolPath, when given, links the tag to
// that path so the resource is filtered by it. userID == "" names nobody: OwnerTransfer then only unlinks from,
// OwnerSole unlinks every holder.
func SetOwnerTx(tx *gorm.DB, resourceID, toolPath, from, userID string, mode OwnerMode) error {
	if resourceID == "" {
		return nil
	}
	name := "owner:" + resourceID
	var t entity.Tag
	err := tx.Where("name = ?", name).First(&t).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if userID == "" {
			return nil // nothing to unlink, nobody to link
		}
		t = entity.Tag{Name: name, IsFilter: true}
		if err := tx.Create(&t).Error; err != nil {
			return err
		}
	case err != nil:
		return err
	}
	if toolPath != "" && userID != "" {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&entity.ToolTag{ToolPath: toolPath, TagID: t.ID}).Error; err != nil {
			return err
		}
	}
	if userID != "" {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&entity.UserTag{UserID: userID, TagID: t.ID}).Error; err != nil {
			return err
		}
	}
	switch mode {
	case OwnerTransfer:
		if from != "" && from != userID {
			return tx.Where("user_id = ? AND tag_id = ?", from, t.ID).Delete(&entity.UserTag{}).Error
		}
	case OwnerSole:
		return tx.Where("tag_id = ? AND user_id <> ?", t.ID, userID).Delete(&entity.UserTag{}).Error
	}
	return nil
}
