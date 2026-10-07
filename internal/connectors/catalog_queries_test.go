package connectors

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// countQueries counts every SELECT the db runs from now on.
func countQueries(t *testing.T, db *gorm.DB) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	inc := func(*gorm.DB) { n.Add(1) }
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:count_query", inc))
	require.NoError(t, db.Callback().Row().After("gorm:row").Register("test:count_row", inc))
	return &n
}

// TestAgentCatalogQueriesDoNotGrowWithRows pins the catalog to a fixed
// number of queries: op toggles, accounts and account tags are read for
// every row at once, not one round trip per row (seconds on a remote
// database for the Team roster, which reads it on every load).
func TestAgentCatalogQueriesDoNotGrowWithRows(t *testing.T) {
	svc, db := newSvcAccountVisDB(t)
	ctx := context.Background()
	tagID := ""
	seed := func(i int) {
		row := seedTwoAccounts(t, svc)
		if tagID == "" {
			tagID = tagRow(t, db, row.ID, "team")
		} else {
			require.NoError(t, db.Create(&entity.ToolTag{ToolPath: "/connectors/" + row.ID, TagID: tagID}).Error)
		}
		require.NoError(t, svc.repo.SetOperation(ctx, row.ID, "whoami", true), fmt.Sprint(i))
	}
	run := func() ([]CatalogEntry, int64) {
		n := countQueries(t, db)
		defer func() {
			_ = db.Callback().Query().Remove("test:count_query")
			_ = db.Callback().Row().Remove("test:count_row")
		}()
		cat, err := svc.AgentCatalog(ctx, "u-alice", []string{tagID}, false)
		require.NoError(t, err)
		return cat, n.Load()
	}

	seed(0)
	cat1, q1 := run()
	require.Len(t, cat1, 1)
	for i := 1; i < 6; i++ {
		seed(i)
	}
	cat6, q6 := run()
	require.Len(t, cat6, 6)
	assert.Equal(t, q1, q6, "catalog queries must not grow with the number of rows (1 row: %d, 6 rows: %d)", q1, q6)
	assert.LessOrEqual(t, q6, int64(6))

	// Batching keeps the per-account rule: alice sees her own account on
	// every row, never bob's.
	for _, e := range cat6 {
		require.Len(t, e.Accounts, 1, e.Row.ID)
		assert.Equal(t, "u-alice", e.Accounts[0].WickUserID)
		assert.Equal(t, []CatalogOp{{Key: "whoami", Name: "Who am I"}}, e.Ops)
	}
}
