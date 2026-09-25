package aimemory

// ai-memory's side of editing a project's memory: write-page, delete-page,
// checkpoints and restore-page.
//
// Every shape below was read off a real ai-memory 2.4.0 run on 2026-09-25,
// the same rule the rest of this package follows:
//
//	$ ai-memory write-page --workspace wick --project demo --path notes/hello.md --body '…'
//	✓ wrote notes/hello.md (page_id=01a0d8dd) under wick/demo
//	$ ai-memory delete-page --workspace wick --project demo --path notes/hello.md
//	✓ deleted notes/hello.md under wick/demo
//	$ ai-memory checkpoints --json -n 3
//	[{"oid":"d544…","short_oid":"d544bc66fa5e","time":1790344784,"summary":"write-page wick/demo: notes/hello.md"}]
//	$ ai-memory restore-page --path notes/hello.md --from d544… --json
//	{"page_id":"01a0d8dd-…","path":"notes/hello.md","restored_from":"d544…","pre_checkpoint":null,"checkpoint":"25d1…"}
//
// Two of the four have no --json mode at all (write-page, delete-page), which
// is why their results carry the backend's own line verbatim rather than a
// structure wick invented: that line names the scope the write actually
// landed in, and after an edit that is the one thing worth reading.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/agents/agentmemory"
)

// scopedArgs builds a subcommand that takes --workspace/--project but NOT
// --json. args() cannot be used for these: it appends --json unconditionally,
// and write-page/delete-page reject the flag.
func scopedArgs(sub string, conn agentmemory.Conn, sc agentmemory.ReadScope, extra ...string) []string {
	out := []string{sub}
	if d := strings.TrimSpace(conn.DataDir); d != "" {
		out = append(out, "--data-dir", d)
	}
	if w := strings.TrimSpace(sc.Workspace); w != "" {
		out = append(out, "--workspace", w)
	}
	if p := strings.TrimSpace(sc.Project); p != "" {
		out = append(out, "--project", p)
	}
	return append(out, extra...)
}

// WritePage runs `write-page`, handing the body over stdin.
//
// `--body -` is the CLI's own way of taking the markdown from stdin, and it
// is used rather than an argv string for two reasons: a page is arbitrary
// text that would have to survive Windows' 32k command line, and a body on
// argv is a body in the process list.
func (s source) WritePage(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope, w agentmemory.PageWrite) (*agentmemory.PageWriteResult, error) {
	path := strings.TrimSpace(w.Path)
	if path == "" {
		return nil, fmt.Errorf("%s write-page needs a path", binName)
	}
	extra := []string{"--path", path, "--body", "-"}
	if t := strings.TrimSpace(w.Title); t != "" {
		extra = append(extra, "--title", t)
	}
	if k := strings.TrimSpace(w.Kind); k != "" {
		extra = append(extra, "--kind", k)
	}
	out, err := s.run(ctx, sc.Dir, env(conn), scopedArgs("write-page", conn, sc, extra...), []byte(w.Body))
	if err != nil {
		return nil, err
	}
	line := lastLine(string(out))
	return &agentmemory.PageWriteResult{
		Path:   path,
		PageID: pageIDFrom(line),
		Output: line,
	}, nil
}

// pageIDFrom pulls the id out of "✓ wrote notes/hello.md (page_id=01a0d8dd)
// under wick/demo". It is best-effort on purpose: the id is a nicety, and a
// future build that drops it from the line must not turn a successful write
// into an error.
func pageIDFrom(line string) string {
	const marker = "page_id="
	i := strings.Index(line, marker)
	if i < 0 {
		return ""
	}
	rest := line[i+len(marker):]
	if j := strings.IndexAny(rest, ") \t"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// DeletePage runs `delete-page`.
//
// The CLI announces its scope resolution on stderr precisely so a
// cross-workspace project-name collision cannot silently route a delete to
// the wrong slot; wick passes the scope explicitly, so there is nothing left
// to resolve.
func (s source) DeletePage(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope, path string) (*agentmemory.PageDeleteResult, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("%s delete-page needs a path", binName)
	}
	out, err := s.run(ctx, sc.Dir, env(conn), scopedArgs("delete-page", conn, sc, "--path", path), nil)
	if err != nil {
		return nil, err
	}
	return &agentmemory.PageDeleteResult{Path: path, Deleted: true, Output: lastLine(string(out))}, nil
}

// checkpointJSON is `checkpoints --json`, verbatim.
type checkpointJSON struct {
	OID      string `json:"oid"`
	ShortOID string `json:"short_oid"`
	Time     int64  `json:"time"`
	Summary  string `json:"summary"`
}

// Checkpoints runs `checkpoints --json`. The list is store-wide — git commits
// cover every project in the wiki — which is why the UI pairs each entry with
// its summary line rather than implying they all belong to the open project.
func (s source) Checkpoints(ctx context.Context, conn agentmemory.Conn, limit int) ([]agentmemory.Checkpoint, error) {
	cmd := []string{"checkpoints"}
	if d := strings.TrimSpace(conn.DataDir); d != "" {
		cmd = append(cmd, "--data-dir", d)
	}
	if limit > 0 {
		cmd = append(cmd, "--limit", strconv.Itoa(limit))
	}
	out, err := s.run(ctx, "", env(conn), append(cmd, "--json"), nil)
	if err != nil {
		return nil, err
	}
	var rows []checkpointJSON
	if err := decode(out, &rows); err != nil {
		return nil, fmt.Errorf("parse %s checkpoints: %w", binName, err)
	}
	list := make([]agentmemory.Checkpoint, 0, len(rows))
	for _, r := range rows {
		list = append(list, agentmemory.Checkpoint{OID: r.OID, ShortOID: r.ShortOID, TimeUnix: r.Time, Summary: r.Summary})
	}
	return list, nil
}

// restoreJSON is `restore-page --json`, verbatim. pre_checkpoint is null when
// the page did not exist before the restore, so it is a pointer: "" and
// "absent" are different answers and only one of them means "nothing was
// replaced".
type restoreJSON struct {
	PageID        string  `json:"page_id"`
	Path          string  `json:"path"`
	RestoredFrom  string  `json:"restored_from"`
	PreCheckpoint *string `json:"pre_checkpoint"`
	Checkpoint    string  `json:"checkpoint"`
}

// RestorePage runs `restore-page --json`.
func (s source) RestorePage(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope, path, from string) (*agentmemory.RestoreResult, error) {
	path, from = strings.TrimSpace(path), strings.TrimSpace(from)
	if path == "" || from == "" {
		return nil, fmt.Errorf("%s restore-page needs a path and a checkpoint", binName)
	}
	out, err := s.run(ctx, sc.Dir, env(conn), scopedArgs("restore-page", conn, sc, "--path", path, "--from", from, "--json"), nil)
	if err != nil {
		return nil, err
	}
	var raw restoreJSON
	if err := decode(out, &raw); err != nil {
		return nil, fmt.Errorf("parse %s restore-page: %w", binName, err)
	}
	res := &agentmemory.RestoreResult{
		PageID:       raw.PageID,
		Path:         raw.Path,
		RestoredFrom: raw.RestoredFrom,
		Checkpoint:   raw.Checkpoint,
	}
	if raw.PreCheckpoint != nil {
		res.PreCheckpoint = *raw.PreCheckpoint
	}
	return res, nil
}
