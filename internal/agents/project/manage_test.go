package project

import "testing"

// Settings and delete belong to the owner alone: a tag grant that opens
// the project (CanAccess) does not let a member reconfigure or remove it.
func TestCanManageIsOwnerOnly(t *testing.T) {
	owned := Meta{ID: "acme", OwnerUserID: "user-a", Tags: []string{"tag-x"}}
	if !CanManage(owned, "user-a", false) {
		t.Error("owner must manage their project")
	}
	if !CanAccess(owned, Access{UserID: "user-b", TagIDs: []string{"tag-x"}}) {
		t.Fatal("setup: the tag grant should open the project to user-b")
	}
	if CanManage(owned, "user-b", false) {
		t.Error("a tag-granted member must not manage the project")
	}
	if CanManage(owned, "user-b", true) {
		t.Error("an admin must not manage a project somebody else owns")
	}
	if CanManage(owned, "", false) {
		t.Error("an empty user id must not match")
	}
	ownerless := Meta{ID: "legacy"}
	if !CanManage(ownerless, "user-admin", true) {
		t.Error("an admin must manage an ownerless project")
	}
	if CanManage(ownerless, "user-b", false) {
		t.Error("a non-admin must not manage an ownerless project")
	}
}
