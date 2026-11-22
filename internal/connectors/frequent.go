package connectors

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// Frequent ops: the operations an agent is shown without discovery — in
// wick_get's first level and in a Team agent's prompt — so a call can go
// straight to wick_execute. The list is curated (pins, mutes, a one-line
// note) on ConnectorOpPref rows and ranked by real use: successful agent
// calls (MCP and code mode) over the last FrequentWindow, the agent's own
// first and everyone's after, recent calls weighing more.

const (
	// FrequentWindow is how far back usage counts.
	FrequentWindow = 30 * 24 * time.Hour
	// FrequentPerConnector caps the ops listed for one connector.
	FrequentPerConnector = 5
	// frequentCacheTTL keeps a ranking in memory: wick_get and spawns ask
	// often, the ranking moves slowly.
	frequentCacheTTL = 5 * time.Minute
)

// OpUsage is one ranked (connector, op) pair.
type OpUsage struct {
	ConnectorID  string  `json:"connector_id"`
	OperationKey string  `json:"operation_key"`
	Calls        int     `json:"calls"`
	Score        float64 `json:"-"`
}

type usageRow struct {
	ConnectorID  string
	OperationKey string
	Calls        int
	Recent       int
}

// usage returns call counts per (connector, op) in the window: total and
// the last 7 days, for agentID ("" = every caller) and, when connectorID
// is set, that one connector only.
func (r *Repo) usage(ctx context.Context, agentID, connectorID string, since time.Time) ([]usageRow, error) {
	recent := time.Now().Add(-7 * 24 * time.Hour)
	q := r.db.WithContext(ctx).Model(&entity.ConnectorRun{}).
		Select("connector_id, operation_key, count(*) AS calls, count(*) FILTER (WHERE started_at > ?) AS recent", recent).
		Where("started_at > ? AND status = ? AND source IN ?", since, entity.ConnectorRunStatusSuccess,
			[]entity.ConnectorRunSource{entity.ConnectorRunSourceMCP, entity.ConnectorRunSourceCodeMode})
	if agentID != "" {
		q = q.Where("agent_id = ?", agentID)
	}
	if connectorID != "" {
		q = q.Where("connector_id = ?", connectorID)
	}
	var rows []usageRow
	err := q.Group("connector_id, operation_key").Order("calls DESC").Limit(500).Scan(&rows).Error
	return rows, err
}

// OpPrefs returns every pref row of one connector instance, by op key.
func (r *Repo) OpPrefs(ctx context.Context, connectorID string) (map[string]entity.ConnectorOpPref, error) {
	var rows []entity.ConnectorOpPref
	q := r.db.WithContext(ctx)
	if connectorID != "" {
		q = q.Where("connector_id = ?", connectorID)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]entity.ConnectorOpPref, len(rows))
	for _, p := range rows {
		out[p.ConnectorID+"/"+p.OperationKey] = p
	}
	return out, nil
}

// UpsertOpPref writes one pref row.
func (r *Repo) UpsertOpPref(ctx context.Context, p *entity.ConnectorOpPref) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(p).Error
}

// frequentCache memoizes rankings per (agent, connector) key.
type frequentCache struct {
	mu   sync.Mutex
	at   map[string]time.Time
	data map[string][]OpUsage
}

func (c *frequentCache) get(k string) ([]OpUsage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.at[k]; ok && time.Since(t) < frequentCacheTTL {
		return c.data[k], true
	}
	return nil, false
}

func (c *frequentCache) put(k string, v []OpUsage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.at == nil {
		c.at, c.data = map[string]time.Time{}, map[string][]OpUsage{}
	}
	c.at[k], c.data[k] = time.Now(), v
}

func (c *frequentCache) reset() {
	c.mu.Lock()
	c.at, c.data = nil, nil
	c.mu.Unlock()
}

var freqCache frequentCache

// rankUsage merges an agent's own usage over everyone's: the agent's rows
// count double, the last 7 days count double again.
func rankUsage(own, all []usageRow) []OpUsage {
	type acc struct {
		calls int
		score float64
	}
	m := map[string]*acc{}
	add := func(rows []usageRow, weight float64) {
		for _, r := range rows {
			k := r.ConnectorID + "/" + r.OperationKey
			a := m[k]
			if a == nil {
				a = &acc{}
				m[k] = a
			}
			if weight > 1 {
				a.calls += r.Calls
			}
			a.score += weight * float64(r.Calls+r.Recent)
		}
	}
	add(own, 2)
	add(all, 1)
	out := make([]OpUsage, 0, len(m))
	for k, a := range m {
		i := strings.LastIndex(k, "/")
		out = append(out, OpUsage{ConnectorID: k[:i], OperationKey: k[i+1:], Calls: a.calls, Score: a.score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ConnectorID+out[i].OperationKey < out[j].ConnectorID+out[j].OperationKey
	})
	return out
}

// UsageRanking is the ranked usage for agentID ("" = every caller),
// optionally limited to one connector. Cached for a few minutes.
func (s *Service) UsageRanking(ctx context.Context, agentID, connectorID string) []OpUsage {
	key := agentID + "|" + connectorID
	if v, ok := freqCache.get(key); ok {
		return v
	}
	since := time.Now().Add(-FrequentWindow)
	all, err := s.repo.usage(ctx, "", connectorID, since)
	if err != nil {
		return nil
	}
	var own []usageRow
	if agentID != "" {
		own, _ = s.repo.usage(ctx, agentID, connectorID, since)
	}
	out := rankUsage(own, all)
	freqCache.put(key, out)
	return out
}

// FrequentOp is one op picked for direct use.
type FrequentOp struct {
	ConnectorID string
	Op          connector.Operation
	Pinned      bool
	Note        string
	Calls       int
}

// PickFrequent chooses up to limit ops of one module for one instance:
// pinned ops first (never muted, destructive allowed only when pinned),
// then the most used ones that are not muted, not destructive and still
// exist on the module. enabled filters ops the instance turned off.
func PickFrequent(mod connector.Module, connectorID string, ranking []OpUsage, prefs map[string]entity.ConnectorOpPref, enabled func(string) bool, limit int) []FrequentOp {
	byKey := map[string]connector.Operation{}
	for _, op := range mod.AllOps() {
		if op.ConfigOnly {
			continue
		}
		if enabled != nil && !enabled(op.Key) {
			continue
		}
		byKey[op.Key] = op
	}
	var out []FrequentOp
	taken := map[string]bool{}
	pinKeys := make([]string, 0)
	for k, p := range prefs {
		if p.ConnectorID == connectorID && p.Pinned && !p.Muted {
			pinKeys = append(pinKeys, k)
		}
	}
	sort.Strings(pinKeys)
	for _, k := range pinKeys {
		p := prefs[k]
		op, ok := byKey[p.OperationKey]
		if !ok || len(out) >= limit {
			continue
		}
		out = append(out, FrequentOp{ConnectorID: connectorID, Op: op, Pinned: true, Note: p.Note})
		taken[op.Key] = true
	}
	for _, u := range ranking {
		if len(out) >= limit {
			break
		}
		if u.ConnectorID != connectorID || taken[u.OperationKey] {
			continue
		}
		op, ok := byKey[u.OperationKey]
		if !ok || op.Destructive {
			continue
		}
		p := prefs[connectorID+"/"+u.OperationKey]
		if p.Muted {
			continue
		}
		out = append(out, FrequentOp{ConnectorID: connectorID, Op: op, Note: p.Note, Calls: u.Calls})
		taken[op.Key] = true
	}
	return out
}

// OpPrefs returns one connector's pref rows keyed "connectorID/opKey"
// ("" = every connector).
func (s *Service) OpPrefs(ctx context.Context, connectorID string) map[string]entity.ConnectorOpPref {
	m, err := s.repo.OpPrefs(ctx, connectorID)
	if err != nil {
		return nil
	}
	return m
}

// OpPrefPatch changes one op's pref; nil fields are left alone.
type OpPrefPatch struct {
	Pinned   *bool
	Muted    *bool
	Note     *string
	Keywords *string
	// Track is a JSON connector.Track; "" clears it.
	Track *string
}

// SetOpPref applies a patch to one op's pref and drops cached rankings.
func (s *Service) SetOpPref(ctx context.Context, connectorID, opKey, by string, patch OpPrefPatch) (entity.ConnectorOpPref, error) {
	cur := s.OpPrefs(ctx, connectorID)[connectorID+"/"+opKey]
	cur.ConnectorID, cur.OperationKey = connectorID, opKey
	if patch.Pinned != nil {
		cur.Pinned = *patch.Pinned
		if cur.Pinned {
			cur.Muted = false
		}
	}
	if patch.Muted != nil {
		cur.Muted = *patch.Muted
		if cur.Muted {
			cur.Pinned = false
		}
	}
	if patch.Note != nil {
		cur.Note = strings.TrimSpace(*patch.Note)
	}
	if patch.Keywords != nil {
		cur.Keywords = strings.TrimSpace(*patch.Keywords)
	}
	if patch.Track != nil {
		cur.Track = strings.TrimSpace(*patch.Track)
	}
	cur.UpdatedBy = by
	cur.UpdatedAt = time.Now()
	if err := s.repo.UpsertOpPref(ctx, &cur); err != nil {
		return cur, err
	}
	freqCache.reset()
	return cur, nil
}

// Signature renders an op's params compactly for a prompt line:
// "op_key(id: string, limit?: number, mode?: fast|slow)".
func Signature(op connector.Operation) string {
	parts := make([]string, 0, len(op.Input))
	for _, f := range op.Input {
		name := f.Key
		if !f.Required || strings.TrimSpace(f.Value) != "" {
			name += "?"
		}
		parts = append(parts, name+": "+sigType(f))
	}
	return op.Key + "(" + strings.Join(parts, ", ") + ")"
}

func sigType(f entity.Config) string {
	switch f.Type {
	case "checkbox", "bool", "boolean":
		return "boolean"
	case "number":
		return "number"
	case "dropdown":
		if opts := optionList(f.Options); len(opts) > 0 {
			return strings.Join(opts, "|")
		}
	}
	return "string"
}

// FrequentPromptMax caps the ops listed in one agent's prompt.
const FrequentPromptMax = 20

// FrequentBlock renders the "Frequent ops" prompt block for a Team agent:
// pinned and most-used ops on the connectors in allow (id → op filter, nil
// = every op), at most FrequentPerConnector each and FrequentPromptMax in
// all. Destructive ops are never listed: they keep the fetch-the-schema
// step. "" when nothing qualifies.
func (s *Service) FrequentBlock(ctx context.Context, agentID string, allow map[string]func(string) bool) string {
	ranking := s.UsageRanking(ctx, agentID, "")
	prefs := s.OpPrefs(ctx, "")
	// Candidate connectors in rank order: pins first, then usage.
	var order []string
	seen := map[string]bool{}
	addConn := func(id string) {
		if _, ok := allow[id]; ok && !seen[id] {
			seen[id] = true
			order = append(order, id)
		}
	}
	pinned := make([]string, 0)
	for _, p := range prefs {
		if p.Pinned && !p.Muted {
			pinned = append(pinned, p.ConnectorID)
		}
	}
	sort.Strings(pinned)
	for _, id := range pinned {
		addConn(id)
	}
	for _, u := range ranking {
		addConn(u.ConnectorID)
	}
	var b strings.Builder
	listed := 0
	for _, id := range order {
		if listed >= FrequentPromptMax {
			break
		}
		row, err := s.repo.Get(ctx, id)
		if err != nil || row.Disabled || !s.TypeEnabled(row.Key) {
			continue
		}
		mod, ok := s.Module(row.Key)
		if !ok {
			continue
		}
		states, err := s.OperationStates(ctx, row.ID, row.Key)
		if err != nil {
			continue
		}
		filter := allow[id]
		enabled := func(op string) bool { return states[op] && (filter == nil || filter(op)) }
		picks := PickFrequent(mod, row.ID, ranking, prefs, enabled, FrequentPerConnector)
		var lines []string
		for _, f := range picks {
			if f.Op.Destructive || listed >= FrequentPromptMax {
				continue
			}
			line := "  - conn:" + row.ID + "/" + Signature(f.Op)
			if f.Note != "" {
				line += " — " + f.Note
			}
			lines = append(lines, line)
			listed++
		}
		if len(lines) == 0 {
			continue
		}
		label := row.Label
		if label == "" {
			label = row.Key
		}
		b.WriteString("- " + label + " (" + row.Key + "):\n" + strings.Join(lines, "\n") + "\n")
	}
	if listed == 0 {
		return ""
	}
	return "## Frequent ops\n" +
		"Your most used (or pinned) operations. Call them with wick_execute directly — tool_id and params are below " +
		"(`?` = optional); a wrong param returns the schema and nothing runs. Any other op: wick_get the connector " +
		"(its groups list what each can do), then the op's schema.\n" + strings.TrimRight(b.String(), "\n")
}
