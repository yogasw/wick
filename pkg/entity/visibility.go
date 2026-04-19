// Package entity exposes shared entity types used in the public wick
// contract (tool/job registration). Most entity types stay in
// internal/entity — only the bits downstream authors touch live here.
package entity

// ToolVisibility controls who can access a tool.
type ToolVisibility string

const (
	VisibilityPublic  ToolVisibility = "public"  // no login needed
	VisibilityPrivate ToolVisibility = "private" // login + approved (optionally filtered by tags)
)
