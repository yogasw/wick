package view

import (
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// UserRow is the view model for a single user row in the admin table.
type UserRow struct {
	User   *entity.User
	TagIDs []string
}

// ToolRow is the view model for a single tool row in the admin table.
type ToolRow struct {
	Tool        tool.Tool
	Visibility  entity.ToolVisibility
	Disabled    bool
	TagIDs      []string
	ConfigCount int
}

// JobRow is the view model for a single job row in the admin jobs table.
type JobRow struct {
	Job         entity.Job
	Disabled    bool
	TagIDs      []string
	ConfigCount int
}

