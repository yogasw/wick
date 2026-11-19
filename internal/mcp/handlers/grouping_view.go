package handlers

import (
	"context"
	"strings"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/pkg/connector"
)

// FlatOpThreshold is the op count up to which wick_get's first level lists
// every op instead of the groups: drilling into groups only pays off once
// listing everything costs more than one extra call.
const FlatOpThreshold = 15

// summaryMax caps one group's capability summary.
const summaryMax = 240

func enabledCount(mod connector.Module, enabled func(string) bool) int {
	n := 0
	for _, op := range mod.AllOps() {
		if enabled(op.Key) {
			n++
		}
	}
	return n
}

// capabilitySummary names what a group can do from its op keys, minus the
// words the group title already says: "Prod-like Services" with ops
// get_prodlike_service_logs and delete_prodlike_service_tool reads
// "logs · delete⚠". Destructive ops carry ⚠.
func capabilitySummary(cat connector.Category, enabled func(string) bool) string {
	drop := map[string]bool{"tool": true, "tools": true}
	for _, w := range splitWords(cat.Title) {
		drop[w] = true
		drop[w+"s"] = true
		drop[strings.TrimSuffix(w, "s")] = true
	}
	var parts []string
	seen := map[string]bool{}
	for _, op := range cat.Ops {
		if !enabled(op.Key) {
			continue
		}
		var keep []string
		for _, w := range splitWords(op.Key) {
			if !drop[w] {
				keep = append(keep, w)
			}
		}
		label := strings.Join(keep, "-")
		if label == "" {
			words := splitWords(op.Key)
			if len(words) == 0 {
				continue
			}
			label = words[0]
		}
		if op.Destructive {
			label += "⚠"
		}
		if seen[label] {
			continue
		}
		seen[label] = true
		parts = append(parts, label)
	}
	out := strings.Join(parts, " · ")
	if len(out) > summaryMax {
		cut := strings.LastIndex(out[:summaryMax], " · ")
		if cut < 0 {
			cut = summaryMax
		}
		out = out[:cut] + " · …"
	}
	return out
}

func splitWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}

// decorateDetail adds the curated layer to a wick_get answer for a saved
// instance: the frequent ops (first level only, grouped connectors only —
// a flat listing already shows every op) and each listed op's note.
func decorateDetail(ctx context.Context, svc *connectors.Service, mod connector.Module, connectorID, selector string, detail *connectorDetail, toolIDFor func(string) string, enabled func(string) bool) {
	if svc == nil || detail == nil {
		return
	}
	prefs := svc.OpPrefs(ctx, connectorID)
	for i := range detail.Tools {
		if p, ok := prefs[connectorID+"/"+opKeyOf(detail.Tools[i].ToolID)]; ok {
			detail.Tools[i].Note = p.Note
		}
	}
	if strings.TrimSpace(selector) != "" || len(detail.Categories) == 0 {
		return
	}
	ranking := svc.UsageRanking(ctx, connectors.AgentIDOf(ctx), connectorID)
	for _, f := range connectors.PickFrequent(mod, connectorID, ranking, prefs, enabled, connectors.FrequentPerConnector) {
		detail.Frequent = append(detail.Frequent, toolDetail{
			ToolID:      toolIDFor(f.Op.Key),
			Name:        f.Op.Name,
			Destructive: f.Op.Destructive,
			Category:    mod.CategoryOf(f.Op.Key),
			Signature:   connectors.Signature(f.Op),
			Note:        f.Note,
		})
	}
}

// opKeyOf pulls the op key out of a tool_id ("conn:<id>/<op>[@acct]").
func opKeyOf(toolID string) string {
	if toolID == "" {
		return ""
	}
	_, op, _, err := ParseToolIDFull(toolID)
	if err != nil {
		return ""
	}
	return op
}
