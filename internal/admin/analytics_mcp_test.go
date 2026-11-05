package admin

import (
	"net/url"
	"testing"
)

// The MCP ops speak the ledger's window words; the page builder reads
// days/from/to. The translation must land on the same range.
func TestPageQueryTranslatesTheLedgerWindow(t *testing.T) {
	cases := []struct {
		in       url.Values
		days     string
		from, to string
	}{
		{url.Values{"window": {"7d"}}, "7", "", ""},
		{url.Values{"window": {"today"}}, "1", "", ""},
		{url.Values{}, "all", "", ""},
		// A time of day is the ledger's business; the page counts days.
		{url.Values{"since": {"2026-03-09T13:30"}, "until": {"2026-03-10"}}, "", "2026-03-09", "2026-03-10"},
	}
	for _, c := range cases {
		got := pageQuery(c.in)
		if got.Get("days") != c.days || got.Get("from") != c.from || got.Get("to") != c.to {
			t.Errorf("pageQuery(%v) = %v, want days=%q from=%q to=%q", c.in, got, c.days, c.from, c.to)
		}
	}
}
