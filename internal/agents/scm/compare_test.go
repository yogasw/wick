package scm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// divergedRepo builds the shape every compare test needs: main and side
// share "init", then each moves on. side renames a file, edits it, adds
// a binary blob and deletes another; main gains one commit of its own so
// the two really have diverged (that is what makes `..` and `...`
// disagree).
//
// Returns the repo dir and the sha of side's own commit.
func divergedRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	gitInit(t, dir) // "init" on main, with README.md

	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("delete me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-qm", "base files")

	mustGit(t, dir, "checkout", "-q", "-b", "side")
	mustGit(t, dir, "mv", "old.txt", "new.txt")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("a\nb\nc\nd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{0, 1, 2, 3, 0, 7}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-qm", "side work")
	sideSHA := headSHA(t, dir)

	// One commit only main has, so base is not an ancestor of head.
	mustGit(t, dir, "checkout", "-q", "main")
	writeCommit(t, dir, "main-only.txt", "main moves on")
	mustGit(t, dir, "checkout", "-q", "side")
	return dir, sideSHA
}

func headSHA(t *testing.T, dir string) string {
	t.Helper()
	out, err := run(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// byPath indexes a compare result for assertions that do not care about
// git's ordering.
func byPath(files []CompareFile) map[string]CompareFile {
	m := map[string]CompareFile{}
	for _, f := range files {
		m[f.Path] = f
	}
	return m
}

// TestCompareRefsFileList covers the whole file-list contract in one
// diverged repo: status letters, the rename source, ± counts, binary as
// -1, and the difference between the two-dot and three-dot ranges.
func TestCompareRefsFileList(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir, _ := divergedRepo(t)

	tests := []struct {
		name     string
		threeDot bool
		// wantPaths are the paths that must be present; wantAbsent the
		// ones that must not.
		wantPaths  []string
		wantAbsent []string
	}{
		{
			name:     "three-dot shows only what head added",
			threeDot: true,
			// main-only.txt landed on main AFTER the merge base, so a
			// merge-base diff must not report it as head deleting it.
			wantPaths:  []string{"new.txt", "blob.bin", "gone.txt"},
			wantAbsent: []string{"main-only.txt"},
		},
		{
			name:      "two-dot also reports what base gained",
			threeDot:  false,
			wantPaths: []string{"new.txt", "blob.bin", "gone.txt", "main-only.txt"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := CompareRefs(ctx, dir, "main", "side", tc.threeDot)
			if err != nil {
				t.Fatalf("CompareRefs: %v", err)
			}
			got := byPath(res.Files)
			for _, p := range tc.wantPaths {
				if _, ok := got[p]; !ok {
					t.Fatalf("missing %q in %+v", p, res.Files)
				}
			}
			for _, p := range tc.wantAbsent {
				if _, ok := got[p]; ok {
					t.Fatalf("%q should not appear in %+v", p, res.Files)
				}
			}
			if res.Base != "main" || res.Head != "side" {
				t.Fatalf("result did not echo the refs: %+v", res)
			}
		})
	}

	// The per-file detail, on the three-dot view the panel defaults to.
	res, err := CompareRefs(ctx, dir, "main", "side", true)
	if err != nil {
		t.Fatal(err)
	}
	got := byPath(res.Files)

	rename := got["new.txt"]
	if rename.Status != "R" {
		t.Fatalf("rename status = %q, want R: %+v", rename.Status, rename)
	}
	if rename.OrigPath != "old.txt" {
		t.Fatalf("rename source = %q, want old.txt", rename.OrigPath)
	}
	// The rename also added a line; the counts must key onto the NEW name.
	if rename.Additions != 1 || rename.Deletions != 0 {
		t.Fatalf("rename counts = +%d -%d, want +1 -0", rename.Additions, rename.Deletions)
	}

	if bin := got["blob.bin"]; bin.Status != "A" || bin.Additions != -1 || bin.Deletions != -1 {
		t.Fatalf("binary file = %+v, want status A with -1/-1", bin)
	}
	if del := got["gone.txt"]; del.Status != "D" || del.Deletions != 1 {
		t.Fatalf("deleted file = %+v, want status D with one deletion", del)
	}
}

// TestCompareRefsAheadBehindAndMergeBase: the header numbers are counted
// from head's side, and the merge base is where the two last agreed.
func TestCompareRefsAheadBehind(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir, _ := divergedRepo(t)

	res, err := CompareRefs(ctx, dir, "main", "side", true)
	if err != nil {
		t.Fatal(err)
	}
	// side has one commit main lacks; main has one side lacks.
	if res.Ahead != 1 || res.Behind != 1 {
		t.Fatalf("ahead/behind = %d/%d, want 1/1", res.Ahead, res.Behind)
	}
	if res.MergeBase == "" {
		t.Fatal("merge base is empty for two branches off one root")
	}
	want, err := run(ctx, dir, "rev-parse", "main~1")
	if err != nil {
		t.Fatal(err)
	}
	if res.MergeBase != strings.TrimSpace(want) {
		t.Fatalf("merge base = %s, want %s", res.MergeBase, strings.TrimSpace(want))
	}

	// Swapping the refs swaps the counts — proof they are not both read
	// off the same side of git's output.
	rev, err := CompareRefs(ctx, dir, "side", "main", true)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Ahead != 1 || rev.Behind != 1 {
		t.Fatalf("swapped ahead/behind = %d/%d, want 1/1", rev.Ahead, rev.Behind)
	}
}

// Unrelated histories are a legitimate comparison: report no merge base
// rather than failing the request.
func TestCompareRefsUnrelatedHistories(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir)
	// An orphan branch shares no commit with main.
	mustGit(t, dir, "checkout", "-q", "--orphan", "alien")
	mustGit(t, dir, "rm", "-q", "-rf", ".")
	writeCommit(t, dir, "alien.txt", "from nowhere")

	res, err := CompareRefs(ctx, dir, "main", "alien", false)
	if err != nil {
		t.Fatalf("CompareRefs on unrelated histories: %v", err)
	}
	if res.MergeBase != "" {
		t.Fatalf("merge base = %q, want empty for unrelated histories", res.MergeBase)
	}
	if len(res.Files) == 0 {
		t.Fatal("no files reported between two unrelated trees")
	}
}

// Refs that could be read as git options must be refused before they
// reach the command line.
func TestCompareRefsRefGuard(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir, _ := divergedRepo(t)

	for _, bad := range []string{"", "   ", "--all", "-n", "has space"} {
		if _, err := CompareRefs(ctx, dir, bad, "side", true); err == nil {
			t.Fatalf("accepted %q as base", bad)
		}
		if _, err := CompareRefs(ctx, dir, "main", bad, true); err == nil {
			t.Fatalf("accepted %q as head", bad)
		}
		if err := RestorePaths(ctx, dir, bad, []string{"new.txt"}); err == nil {
			t.Fatalf("restore accepted %q as ref", bad)
		}
		if err := ResetTo(ctx, dir, bad, "hard"); err == nil {
			t.Fatalf("reset accepted %q as ref", bad)
		}
		if _, err := RevertCommit(ctx, dir, bad, false); err == nil {
			t.Fatalf("revert accepted %q as sha", bad)
		}
	}
}

// TestRestorePaths: a file comes back from another ref, and a path that
// tries to leave the repo never reaches git.
func TestRestorePaths(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir, _ := divergedRepo(t) // checked out on side

	// side edited new.txt (renamed from old.txt). Restore the pre-rename
	// name from main and both the old content and the old path are back.
	if err := RestorePaths(ctx, dir, "main", []string{"old.txt"}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "old.txt"))
	if err != nil {
		t.Fatalf("restored file missing: %v", err)
	}
	if string(b) != "a\nb\nc\n" {
		t.Fatalf("restored content = %q, want main's version", b)
	}
	// It is staged, not just on disk — `git checkout <ref> -- <path>`
	// writes the index too, which is what makes it a rollback rather
	// than an edit.
	st, err := Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var staged bool
	for _, ch := range st.Changes {
		if ch.Path == "old.txt" && ch.Staged {
			staged = true
		}
	}
	if !staged {
		t.Fatalf("restored path not staged: %+v", st.Changes)
	}

	if err := RestorePaths(ctx, dir, "main", nil); err == nil {
		t.Fatal("restore accepted an empty path list")
	}
	for _, bad := range []string{"../escape.txt", "/etc/passwd", ""} {
		if err := RestorePaths(ctx, dir, "main", []string{bad}); err == nil {
			t.Fatalf("restore accepted path %q", bad)
		}
	}
}

// TestRevertCommit: the inverse commit lands, and a revert that cannot
// apply reports git's own message instead of a bare exit code.
func TestRevertCommit(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir, sideSHA := divergedRepo(t)

	// Reverting side's own commit undoes it: the deleted file returns and
	// the added binary goes away.
	if _, err := RevertCommit(ctx, dir, sideSHA, false); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatalf("revert did not bring the deleted file back: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blob.bin")); !os.IsNotExist(err) {
		t.Fatalf("revert left the added file behind: %v", err)
	}
	// It committed, so the tree is clean again.
	st, err := Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Changes) != 0 {
		t.Fatalf("revert left changes behind: %+v", st.Changes)
	}

	// no_commit stages the inverse instead of committing it.
	head := headSHA(t, dir)
	if _, err := RevertCommit(ctx, dir, head, true); err != nil {
		t.Fatalf("revert -n: %v", err)
	}
	if headSHA(t, dir) != head {
		t.Fatal("revert with no_commit created a commit")
	}
	st, err = Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Changes) == 0 {
		t.Fatal("revert with no_commit staged nothing")
	}
}

// A revert that conflicts must surface the file names git printed on
// stdout — stderr alone says only that something failed.
func TestRevertCommitConflictMessage(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-qm", "first")
	target := headSHA(t, dir)
	// Rewrite the same line, so undoing "first" no longer applies.
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-qm", "second")

	_, err := RevertCommit(ctx, dir, target, false)
	if err == nil {
		t.Fatal("conflicting revert reported success")
	}
	if !strings.Contains(err.Error(), "f.txt") {
		t.Fatalf("conflict error does not name the file: %v", err)
	}
}

// TestResetTo covers each allowed mode's distinct effect plus the
// rejection of anything else.
func TestResetTo(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()

	tests := []struct {
		mode string
		// wantStaged/wantUnstaged describe the state of the file the
		// reset commit added, seen from the branch tip one back.
		wantStaged   bool
		wantUnstaged bool
	}{
		{mode: "soft", wantStaged: true},
		{mode: "mixed", wantUnstaged: true},
		{mode: "hard"},
		{mode: "", wantUnstaged: true}, // empty defaults to mixed
	}
	for _, tc := range tests {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			dir := t.TempDir()
			gitInit(t, dir)
			writeCommit(t, dir, "extra.txt", "to be reset away")

			if err := ResetTo(ctx, dir, "HEAD~1", tc.mode); err != nil {
				t.Fatalf("reset --%s: %v", tc.mode, err)
			}
			// The branch moved in every mode.
			out, err := run(ctx, dir, "log", "--oneline")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "to be reset away") {
				t.Fatalf("reset --%s did not move the branch: %s", tc.mode, out)
			}
			st, err := Status(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			var staged, unstaged bool
			for _, ch := range st.Changes {
				if ch.Path != "extra.txt" {
					continue
				}
				staged = staged || ch.Staged
				unstaged = unstaged || ch.Unstaged
			}
			if staged != tc.wantStaged || unstaged != tc.wantUnstaged {
				t.Fatalf("reset --%s left staged=%v unstaged=%v, want %v/%v (%+v)",
					tc.mode, staged, unstaged, tc.wantStaged, tc.wantUnstaged, st.Changes)
			}
			// hard is the only mode that takes the file off disk.
			_, statErr := os.Stat(filepath.Join(dir, "extra.txt"))
			if tc.mode == "hard" && !os.IsNotExist(statErr) {
				t.Fatalf("reset --hard left the file on disk: %v", statErr)
			}
			if tc.mode != "hard" && statErr != nil {
				t.Fatalf("reset --%s removed the file: %v", tc.mode, statErr)
			}
		})
	}

	dir := t.TempDir()
	gitInit(t, dir)
	for _, bad := range []string{"merge", "keep", "HARD", "--hard", "nonsense"} {
		if err := ResetTo(ctx, dir, "HEAD", bad); err == nil {
			t.Fatalf("accepted reset mode %q", bad)
		}
	}
}

func findFile(files []CompareFile, path string) *CompareFile {
	for i := range files {
		if files[i].Path == path {
			return &files[i]
		}
	}
	return nil
}

// The working tree as a head: what main...HEAD commits PLUS what is not
// committed yet, and the staged-only variant that leaves unstaged edits out.
func TestCompareRefsWorkingSides(t *testing.T) {
	dir, _ := divergedRepo(t)
	ctx := context.Background()
	// One staged edit to README.md, then one more unstaged line on top.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "README.md")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("staged\nunstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wt, err := CompareRefs(ctx, dir, "main", WorktreeRef, true)
	if err != nil {
		t.Fatal(err)
	}
	wtReadme := findFile(wt.Files, "README.md")
	if wtReadme == nil || findFile(wt.Files, "blob.bin") == nil || findFile(wt.Files, "new.txt") == nil {
		t.Fatalf("worktree compare should carry both committed and uncommitted files, got %+v", wt.Files)
	}
	// Merge-base mode: main-only.txt is base's own, not ours.
	if findFile(wt.Files, "main-only.txt") != nil {
		t.Fatalf("merge-base worktree compare must not list main's own commit")
	}
	if wt.Ahead != 1 || len(wt.Commits) != 1 || wt.Commits[0].Subject != "side work" {
		t.Fatalf("commits: ahead=%d commits=%+v", wt.Ahead, wt.Commits)
	}

	st, err := CompareRefs(ctx, dir, "main", StagedRef, true)
	if err != nil {
		t.Fatal(err)
	}
	// The unstaged line is one more addition on the worktree side.
	if f := findFile(st.Files, "README.md"); f == nil || f.Additions != wtReadme.Additions-1 {
		t.Fatalf("staged compare must leave the unstaged line out, got %+v vs worktree %+v", f, wtReadme)
	}

	if _, err := CompareRefs(ctx, dir, WorktreeRef, "main", true); err == nil {
		t.Fatal("working tree as base should be refused")
	}

	got, err := WorkingFile(ctx, dir, WorktreeRef, "README.md")
	if err != nil || !strings.Contains(got, "unstaged") {
		t.Fatalf("WorkingFile(worktree) = %q, %v", got, err)
	}
	got, err = WorkingFile(ctx, dir, StagedRef, "README.md")
	if err != nil || got != "staged\n" {
		t.Fatalf("WorkingFile(staged) = %q, %v", got, err)
	}
	if got, err := WorkingFile(ctx, dir, WorktreeRef, "gone.txt"); err != nil || got != "" {
		t.Fatalf("a deleted file should read empty, got %q, %v", got, err)
	}
}

// Last N commits is HEAD~N..HEAD; the commit list is newest first.
func TestCompareRefsLastNCommits(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	writeCommit(t, dir, "a.txt", "one")
	writeCommit(t, dir, "b.txt", "two")
	writeCommit(t, dir, "c.txt", "three")
	res, err := CompareRefs(context.Background(), dir, "HEAD~2", "HEAD", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 || findFile(res.Files, "a.txt") != nil {
		t.Fatalf("HEAD~2..HEAD should be b.txt and c.txt, got %+v", res.Files)
	}
	if len(res.Commits) != 2 || res.Commits[0].Subject != "three" || res.Commits[0].SHA == "" {
		t.Fatalf("commits = %+v", res.Commits)
	}
}

func TestValidateCompareRef(t *testing.T) {
	dir, side := divergedRepo(t)
	ctx := context.Background()
	for _, ok := range []string{"main", "side", "HEAD~1", side, side[:7], WorktreeRef, StagedRef} {
		if err := ValidateCompareRef(ctx, dir, ok); err != nil {
			t.Errorf("%q: unexpected error %v", ok, err)
		}
	}
	for _, bad := range []string{"", "--output=/tmp/x", "-p", "main..side", "main...side", "nope", "main side", "HEAD:README.md"} {
		if err := ValidateCompareRef(ctx, dir, bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	// A refused ref never reaches a diff.
	if _, err := CompareRefs(ctx, dir, "--output=x", "side", true); err == nil {
		t.Fatal("an option-shaped base must be refused")
	}
}
