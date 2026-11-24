package wickmanager

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/admin"
	"github.com/yogasw/wick/internal/channelidentity"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// people.go: the Users, Tags and sign-up settings pages as ops, plus where
// account notices go. Every op is ADMIN ONLY.

// PeopleSource is the admin handler's manage API. Wired after the connector
// is registered, so Deps carries a func returning it.
type PeopleSource interface {
	ManageListUsers(ctx context.Context, status, query string) ([]admin.ManageUser, error)
	ManageSetApproved(ctx context.Context, userRef string, approved bool) (*entity.User, error)
	ManageSetRole(ctx context.Context, userRef, role string) (*entity.User, error)
	ManageSetUserTags(ctx context.Context, userRef string, set, add, remove []string, replace bool) ([]string, error)
	ManageListTags(ctx context.Context) ([]map[string]any, error)
	ManageCreateTag(ctx context.Context, name, description string, isGroup, isFilter bool) (map[string]any, error)
	ManageUpdateTag(ctx context.Context, ref string, p admin.TagPatch) (map[string]any, error)
	ManageDeleteTag(ctx context.Context, ref string) (string, error)
	ManageGetSignup(ctx context.Context) (admin.SignupSettings, error)
	ManageSetSignup(ctx context.Context, p admin.SignupPatch) (admin.SignupSettings, error)
}

var errPeopleUnavailable = errors.New("user management unavailable in this run mode")

type userListInput struct {
	Status string `wick:"dropdown=all|pending|approved;default=all;desc=pending = waiting for approval."`
	Query  string `wick:"desc=Part of a name or email to narrow the list."`
}

type userRefInput struct {
	User string `wick:"required;desc=User id or exact email."`
}

type userRoleInput struct {
	User string `wick:"required;desc=User id or exact email."`
	Role string `wick:"required;dropdown=user|admin;desc=New role. Demoting the last admin is refused."`
}

type userTagsInput struct {
	User   string `wick:"required;desc=User id or exact email. Must be approved first."`
	Set    string `wick:"desc=Comma list of tag names or ids that REPLACES the user's tags (system tags are kept). Empty = keep and use add/remove."`
	Add    string `wick:"desc=Comma list of tag names or ids to add."`
	Remove string `wick:"desc=Comma list of tag names or ids to remove."`
}

type tagCreateInput struct {
	Name        string `wick:"required;desc=Tag name."`
	Description string `wick:"desc=What the tag is for."`
	IsGroup     string `wick:"dropdown=false|true;default=false;desc=Group tag: shown as a group on the home page."`
	IsFilter    string `wick:"dropdown=false|true;default=false;desc=Filter tag: gates access (a person needs the tag to see items carrying it)."`
}

type tagUpdateInput struct {
	Tag         string `wick:"required;desc=Tag id or name."`
	Name        string `wick:"desc=New name. Empty = keep."`
	Description string `wick:"desc=New description. Empty = keep."`
	IsGroup     string `wick:"dropdown=keep|true|false;default=keep"`
	IsFilter    string `wick:"dropdown=keep|true|false;default=keep"`
	SortOrder   string `wick:"desc=New sort order (number). Empty = keep."`
}

type tagDeleteInput struct {
	Tag string `wick:"required;desc=Tag id or name. System tags cannot be deleted."`
}

type signupSetInput struct {
	AutoRegister       string `wick:"dropdown=keep|true|false;default=keep;desc=true: a first Google sign-in creates the account. false: only existing accounts and admin emails can sign in."`
	AutoApproveDomains string `wick:"desc=Comma list of email domains approved at sign-in without an admin (e.g. example.com). Exact domain, no subdomains. Pass - to clear. Empty = keep."`
	AllowedDomains     string `wick:"desc=Comma list of domains allowed to sign in at all. Pass - to clear (allow every domain). Empty = keep."`
	AutoApproveTags    string `wick:"desc=Comma list of tag names or ids given to an account when it is auto-approved (Google sign-in or a chat channel such as Slack), e.g. the tag that opens Agents. Replaces the list. Pass - to clear. Empty = keep."`
}

type alertsSetInput struct {
	Targets string `wick:"required;desc=Comma list: all · off · push · slack · <instance key> · <instance key>#<channel id>. Example: push,slack:acme#C01TESTCHAN posts once into that channel and pushes, without DMs. The bot must be a member of the channel."`
}

func peopleOps(h *handlers) []connector.Operation {
	return []connector.Operation{
		connector.Op("user_list", "List Users",
			"List wick accounts with role, approval and tags. status=pending lists who waits for approval. Access: ADMIN ONLY. UI: <app_url>/admin/users.",
			userListInput{}, h.userList, wickdocs.Docs{}),
		connector.Op("user_approve", "Approve User",
			"Approve a pending account; the user and the alert targets get the usual notice. Returns {ok, user}. Access: ADMIN ONLY. UI: <app_url>/admin/users.",
			userRefInput{}, h.userApprove, wickdocs.Docs{}),
		connector.OpDestructive("user_unapprove", "Revoke User Approval",
			"Revoke an account's approval. Also removes all its tags. Access: ADMIN ONLY. UI: <app_url>/admin/users.",
			userRefInput{}, h.userUnapprove, wickdocs.Docs{}),
		connector.Op("user_set_role", "Set User Role",
			"Make an account admin or user. Access: ADMIN ONLY. UI: <app_url>/admin/users.",
			userRoleInput{}, h.userSetRole, wickdocs.Docs{}),
		connector.Op("user_set_tags", "Set User Tags",
			"Give or take tags from an approved account, by tag name or id. Returns the tags it has afterwards. Access: ADMIN ONLY. UI: <app_url>/admin/users.",
			userTagsInput{}, h.userSetTags, wickdocs.Docs{}),
		connector.Op("tag_list", "List Tags",
			"List tags with flags and how many users / items carry each. Access: ADMIN ONLY. UI: <app_url>/admin/tags.",
			emptyInput{}, h.tagList, wickdocs.Docs{}),
		connector.Op("tag_create", "Create Tag",
			"Create a tag. Access: ADMIN ONLY. UI: <app_url>/admin/tags.",
			tagCreateInput{}, h.tagCreate, wickdocs.Docs{}),
		connector.Op("tag_update", "Update Tag",
			"Rename a tag or change its description / flags / order. Access: ADMIN ONLY. UI: <app_url>/admin/tags.",
			tagUpdateInput{}, h.tagUpdate, wickdocs.Docs{}),
		connector.OpDestructive("tag_delete", "Delete Tag",
			"Delete a tag; everyone and everything carrying it loses it. Access: ADMIN ONLY. UI: <app_url>/admin/tags.",
			tagDeleteInput{}, h.tagDelete, wickdocs.Docs{}),
		connector.Op("signup_get", "Get Sign-up Settings",
			"Google sign-in sign-up settings: auto_register, auto_approve_domains, auto_approve_tags, allowed_domains. The auto-approve domains and tags apply to chat-channel registrations (Slack) too. Access: ADMIN ONLY. UI: <app_url>/admin/advanced/sso.",
			emptyInput{}, h.signupGet, wickdocs.Docs{}),
		connector.Op("signup_set", "Set Sign-up Settings",
			"Change auto-register, the auto-approve / allowed email domains, and the tags auto-approved accounts receive. Auto-approved accounts are plain users, never admin. Access: ADMIN ONLY. UI: <app_url>/admin/advanced/sso.",
			signupSetInput{}, h.signupSet, wickdocs.Docs{}),
		connector.Op("user_alerts_get", "Get Account Alert Targets",
			"Where admin notices about accounts (new user waiting, user approved, account needs linking) go, plus the chat doors (instance keys) the caller is linked on, to build a target from. Access: ADMIN ONLY.",
			emptyInput{}, h.alertsGet, wickdocs.Docs{}),
		connector.Op("user_alerts_set", "Set Account Alert Targets",
			"Set where admin notices about accounts go, so they do not fan out to every admin on every door. Access: ADMIN ONLY.",
			alertsSetInput{}, h.alertsSet, wickdocs.Docs{}),
	}
}

// peopleCall is the shared gate: admin only.
func (h *handlers) peopleCall(c *connector.Ctx, op string, run func(PeopleSource, context.Context) (any, error)) (out any, err error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, op, c.Inputs(), err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	var src PeopleSource
	if h.deps.People != nil {
		src = h.deps.People()
	}
	if src == nil {
		err = errPeopleUnavailable
		return nil, err
	}
	out, err = run(src, ctx)
	return out, err
}

func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// triBool reads keep|true|false; nil = keep.
func triBool(s string) *bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1", "on":
		v := true
		return &v
	case "false", "no", "0", "off":
		v := false
		return &v
	}
	return nil
}

// keepOrClear: "" = keep (nil), "-" = clear, else the value.
func keepOrClear(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if s == "-" {
		s = ""
	}
	return &s
}

func userOut(u *entity.User) map[string]any {
	return map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "role": string(u.Role), "approved": u.Approved}
}

func (h *handlers) userList(c *connector.Ctx) (any, error) {
	return h.peopleCall(c, "user_list", func(src PeopleSource, ctx context.Context) (any, error) {
		status := c.Input("status")
		if status == "all" {
			status = ""
		}
		users, err := src.ManageListUsers(ctx, status, c.Input("query"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"count": len(users), "users": users}, nil
	})
}

func (h *handlers) userApprove(c *connector.Ctx) (any, error) {
	return h.peopleCall(c, "user_approve", func(src PeopleSource, ctx context.Context) (any, error) {
		u, err := src.ManageSetApproved(ctx, c.Input("user"), true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "user": userOut(u)}, nil
	})
}

func (h *handlers) userUnapprove(c *connector.Ctx) (any, error) {
	return h.peopleCall(c, "user_unapprove", func(src PeopleSource, ctx context.Context) (any, error) {
		u, err := src.ManageSetApproved(ctx, c.Input("user"), false)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "user": userOut(u)}, nil
	})
}

func (h *handlers) userSetRole(c *connector.Ctx) (any, error) {
	return h.peopleCall(c, "user_set_role", func(src PeopleSource, ctx context.Context) (any, error) {
		u, err := src.ManageSetRole(ctx, c.Input("user"), c.Input("role"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "user": userOut(u)}, nil
	})
}

func (h *handlers) userSetTags(c *connector.Ctx) (any, error) {
	return h.peopleCall(c, "user_set_tags", func(src PeopleSource, ctx context.Context) (any, error) {
		set := splitList(c.Input("set"))
		add, remove := splitList(c.Input("add")), splitList(c.Input("remove"))
		if len(set) == 0 && len(add) == 0 && len(remove) == 0 {
			return nil, errors.New("pass set, add or remove")
		}
		tags, err := src.ManageSetUserTags(ctx, c.Input("user"), set, add, remove, len(set) > 0)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "tags": tags}, nil
	})
}

func (h *handlers) tagList(c *connector.Ctx) (any, error) {
	return h.peopleCall(c, "tag_list", func(src PeopleSource, ctx context.Context) (any, error) {
		return src.ManageListTags(ctx)
	})
}

func (h *handlers) tagCreate(c *connector.Ctx) (any, error) {
	return h.peopleCall(c, "tag_create", func(src PeopleSource, ctx context.Context) (any, error) {
		return src.ManageCreateTag(ctx, c.Input("name"), c.Input("description"),
			c.InputBool("is_group"), c.InputBool("is_filter"))
	})
}

func (h *handlers) tagUpdate(c *connector.Ctx) (any, error) {
	return h.peopleCall(c, "tag_update", func(src PeopleSource, ctx context.Context) (any, error) {
		p := admin.TagPatch{IsGroup: triBool(c.Input("is_group")), IsFilter: triBool(c.Input("is_filter"))}
		if v := strings.TrimSpace(c.Input("name")); v != "" {
			p.Name = &v
		}
		if v := c.Input("description"); strings.TrimSpace(v) != "" {
			p.Description = &v
		}
		if v := strings.TrimSpace(c.Input("sort_order")); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, errors.New("sort_order must be a number")
			}
			p.SortOrder = &n
		}
		return src.ManageUpdateTag(ctx, c.Input("tag"), p)
	})
}

func (h *handlers) tagDelete(c *connector.Ctx) (any, error) {
	return h.peopleCall(c, "tag_delete", func(src PeopleSource, ctx context.Context) (any, error) {
		name, err := src.ManageDeleteTag(ctx, c.Input("tag"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "deleted": name}, nil
	})
}

func (h *handlers) signupGet(c *connector.Ctx) (any, error) {
	return h.peopleCall(c, "signup_get", func(src PeopleSource, ctx context.Context) (any, error) {
		return src.ManageGetSignup(ctx)
	})
}

func (h *handlers) signupSet(c *connector.Ctx) (any, error) {
	return h.peopleCall(c, "signup_set", func(src PeopleSource, ctx context.Context) (any, error) {
		p := admin.SignupPatch{
			AutoRegister:       triBool(c.Input("auto_register")),
			AutoApproveDomains: keepOrClear(c.Input("auto_approve_domains")),
			AllowedDomains:     keepOrClear(c.Input("allowed_domains")),
		}
		if raw := keepOrClear(c.Input("auto_approve_tags")); raw != nil {
			refs := []string{}
			for _, r := range strings.Split(*raw, ",") {
				if r = strings.TrimSpace(r); r != "" {
					refs = append(refs, r)
				}
			}
			p.AutoApproveTags = &refs
		}
		if p.AutoRegister == nil && p.AutoApproveDomains == nil && p.AllowedDomains == nil && p.AutoApproveTags == nil {
			return nil, errors.New("nothing to change: pass auto_register, auto_approve_domains, auto_approve_tags or allowed_domains")
		}
		return src.ManageSetSignup(ctx, p)
	})
}

func (h *handlers) alertsGet(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, "user_alerts_get", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	raw := h.deps.Configs.GetOwned(channelidentity.AlertConfigOwner, channelidentity.AlertConfigTargets)
	out := map[string]any{"targets": raw, "parsed": describeTargets(raw)}
	if h.deps.AlertDoors != nil {
		out["your_doors"] = h.deps.AlertDoors(ctx, user.ID)
	}
	return out, nil
}

func (h *handlers) alertsSet(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	raw := strings.TrimSpace(c.Input("targets"))
	user, err := requireAdmin(ctx)
	var before string
	defer func() {
		logOpDiff(user, "user_alerts_set", map[string]string{"targets": raw}, before, raw, err, time.Since(start))
	}()
	if err != nil {
		return nil, err
	}
	before = h.deps.Configs.GetOwned(channelidentity.AlertConfigOwner, channelidentity.AlertConfigTargets)
	if err = h.deps.Configs.SetOwned(ctx, channelidentity.AlertConfigOwner, channelidentity.AlertConfigTargets, raw); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "before": before, "targets": raw, "parsed": describeTargets(raw)}, nil
}

// describeTargets explains the setting in words so a typo is visible.
func describeTargets(raw string) []string {
	t := channelidentity.ParseAlertTargets(raw)
	switch {
	case t.Off:
		return []string{"off: no admin notices"}
	case t.All:
		return []string{"all: every admin, browser push + every chat DM"}
	}
	out := []string{}
	if t.Push {
		out = append(out, "browser push to every admin")
	}
	for _, d := range t.Doors {
		out = append(out, "DM every admin via "+d)
	}
	for _, r := range t.Rooms {
		out = append(out, "post once into "+r.RoomID+" via "+r.InstanceKey)
	}
	if len(out) == 0 {
		out = append(out, "nothing recognised: no admin notices will be sent")
	}
	return out
}
