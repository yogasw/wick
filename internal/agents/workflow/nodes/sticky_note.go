package nodes

import (
	"context"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

type stickyNoteSchema struct {
	Content string `wick:"key=content;textarea;desc=Markdown body: block title + what this group of nodes does and why"`
	Color   string `wick:"key=color;desc=yellow | green | blue | purple | red | gray (empty = yellow)"`
	Width   int    `wick:"key=width;desc=Width in canvas px (0 = 240)"`
	Height  int    `wick:"key=height;desc=Height in canvas px (0 = 160)"`
}

func (e *StickyNoteExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Category:    engine.CategoryLogic,
		Label:       "Sticky note",
		Badge:       "note",
		Description: "Canvas-only markdown annotation drawn behind nodes to frame and explain a block of steps. Never executes, takes no edges.",
		WhenToUse:   "Group a lane/block of nodes: put the title + summary (what for + why) in content and size it to cover the block. content is the board title; for annotations placed freely on it set texts [{id,content,x,y,width,color,size}] = small sticky cards (x/y/width relative 0..1, color presets, size sm|md|lg; e.g. explanation {x:0.55,y:0.15,width:0.4,color:blue}). Move/resize with workflow_move_nodes / workflow_update_node {width,height}.",
		Schema:      integration.StructSchema(stickyNoteSchema{}),
	}
}

// StickyNoteExecutor exists only so sticky_note lands in the node
// catalog + palette. The validator rejects edges and entry_node on a
// note, so the engine never walks into one; Execute is a no-op guard.
type StickyNoteExecutor struct{}

// NewStickyNoteExecutor builds the sticky note (no-op) executor.
func NewStickyNoteExecutor() *StickyNoteExecutor { return &StickyNoteExecutor{} }

// Execute does nothing — notes are annotations.
func (e *StickyNoteExecutor) Execute(ctx context.Context, n workflow.Node, _ *workflow.RunContext) (workflow.NodeOutput, error) {
	return workflow.NodeOutput{}, nil
}
