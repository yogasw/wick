// Package shardedlog is a generic append-only JSONL store split
// across date-bucketed shards. Designed for "give me the latest N
// rows" queries without scanning every per-row directory — useful
// for any feature that wants a paginated history view (workflow
// runs, agent sessions, future log mirrors, …).
//
// Layout on disk:
//
//	<Dir>/
//	  2026-05-15-01.jsonl     (≤ ShardMax rows)
//	  2026-05-15-02.jsonl
//	  2026-05-16-01.jsonl
//
// Newest rows sit at the END of the latest shard. Reading newest-
// first = list shards descending + scan each shard from bottom up.
//
// Cost is bounded by ShardMax regardless of total history size:
// each page request reads one shard (~100 rows * ~100 bytes = ~10KB).
// Append cost is also bounded — we touch the latest shard only.
package shardedlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultShardMax is the per-shard row cap. Each page request reads
// a single shard so this directly bounds scan cost.
const DefaultShardMax = 100

// Store is a sharded append-only JSONL log. Callers create one
// instance per logical stream (e.g. per workflow id, per session
// id). Concurrent appends to the same Store are serialised via the
// internal mutex.
//
// Zero-value works except Dir is required.
type Store[T any] struct {
	// Dir is the on-disk directory holding shard files. Created on
	// first Append if it doesn't exist.
	Dir string
	// ShardMax bounds rows per shard file. Defaults to DefaultShardMax.
	ShardMax int
	// Now overrides time.Now for tests so the per-date bucket name
	// is deterministic. nil = time.Now().UTC().
	Now func() time.Time

	mu sync.Mutex
}

func (s *Store[T]) shardMax() int {
	if s.ShardMax > 0 {
		return s.ShardMax
	}
	return DefaultShardMax
}

func (s *Store[T]) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// Append writes one entry to the active shard, rolling to the next
// shard when the current one hits ShardMax rows. Serial — safe for
// concurrent callers via the per-Store mutex.
func (s *Store[T]) Append(entry T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Dir == "" {
		return errors.New("shardedlog: Dir is required")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("shardedlog: mkdir: %w", err)
	}
	shard, err := s.pickShard()
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, shard)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("shardedlog: open: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(entry); err != nil {
		return fmt.Errorf("shardedlog: encode: %w", err)
	}
	return nil
}

// Page returns one page of entries, newest first. page is 1-based.
// pageSize defaults to ShardMax (= one shard per page).
// hasMore=true means older pages exist.
func (s *Store[T]) Page(page, pageSize int) ([]T, bool, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = s.shardMax()
	}
	shards, err := listShards(s.Dir)
	if err != nil {
		return nil, false, err
	}
	// Newest first.
	reverse(shards)
	skip := (page - 1) * pageSize
	out := make([]T, 0, pageSize)
	for _, name := range shards {
		entries, err := readShardReverse[T](filepath.Join(s.Dir, name))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if skip > 0 {
				skip--
				continue
			}
			if len(out) >= pageSize {
				return out, true, nil
			}
			out = append(out, e)
		}
	}
	return out, false, nil
}

// pickShard finds the active shard name to append to. Reuses today's
// latest shard if it's still under the cap; otherwise rolls to the
// next seq for today.
func (s *Store[T]) pickShard() (string, error) {
	today := s.now().Format("2006-01-02")
	names, err := listShards(s.Dir)
	if err != nil {
		return "", err
	}
	maxSeq := 0
	var latestToday string
	for _, n := range names {
		if !strings.HasPrefix(n, today) {
			continue
		}
		seq := parseSeq(n, today)
		if seq > maxSeq {
			maxSeq = seq
			latestToday = n
		}
	}
	if latestToday != "" {
		cnt, err := countLines(filepath.Join(s.Dir, latestToday))
		if err == nil && cnt < s.shardMax() {
			return latestToday, nil
		}
	}
	return fmt.Sprintf("%s-%02d.jsonl", today, maxSeq+1), nil
}

// listShards returns the file names matching `*.jsonl` under dir,
// sorted ascending. Missing dir → empty list (not an error).
func listShards(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// parseSeq pulls the trailing `NN` out of `YYYY-MM-DD-NN.jsonl`.
// Returns 0 if the name doesn't match the expected pattern.
func parseSeq(name, datePrefix string) int {
	rest := strings.TrimPrefix(name, datePrefix+"-")
	rest = strings.TrimSuffix(rest, ".jsonl")
	if rest == "" || rest == name {
		return 0
	}
	n := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	n := 0
	for scanner.Scan() {
		n++
	}
	return n, scanner.Err()
}

// readShardReverse parses a JSONL file and returns rows newest-first
// (file is append-only so the newest line is the last — we read all
// then reverse).
func readShardReverse[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	var entries []T
	for scanner.Scan() {
		var e T
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			// Skip corrupt lines — single bad row shouldn't blank a
			// whole shard.
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	reverse(entries)
	return entries, nil
}

func reverse[T any](xs []T) {
	for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 {
		xs[i], xs[j] = xs[j], xs[i]
	}
}
