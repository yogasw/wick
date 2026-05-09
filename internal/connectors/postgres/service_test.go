package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSelectOnly(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		wantErr bool
	}{
		{"simple SELECT", "SELECT id, name FROM users", false},
		{"SELECT with WHERE", "SELECT * FROM orders WHERE status = 'open'", false},
		{"CTE", "WITH cte AS (SELECT id FROM users) SELECT * FROM cte", false},
		{"empty", "", true},
		{"INSERT", "INSERT INTO users VALUES (1,'x')", true},
		{"UPDATE", "UPDATE users SET name='x'", true},
		{"DELETE", "DELETE FROM users", true},
		{"DROP", "DROP TABLE users", true},
		{"comment bypass", "-- trick\nDROP TABLE users", true},
		{"block comment bypass", "/* trick */ DELETE FROM users", true},
		{"TRUNCATE", "TRUNCATE users", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSelectOnly(tt.sql)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAppendLimit(t *testing.T) {
	assert.Equal(t, "SELECT * FROM users LIMIT 100", appendLimit("SELECT * FROM users", 100))
	assert.Equal(t, "SELECT * FROM users LIMIT 5", appendLimit("SELECT * FROM users LIMIT 5", 100))
	assert.Equal(t, "SELECT * FROM users LIMIT 50", appendLimit("SELECT * FROM users;", 50))
}

func TestStripLeadingComments(t *testing.T) {
	assert.Equal(t, "SELECT 1", stripLeadingComments("-- comment\nSELECT 1"))
	assert.Equal(t, "SELECT 1", stripLeadingComments("/* block */ SELECT 1"))
	assert.Equal(t, "SELECT 1", stripLeadingComments("SELECT 1"))
	assert.Equal(t, "", stripLeadingComments("-- only comment"))
}

func TestMeta(t *testing.T) {
	m := Meta()
	assert.Equal(t, Key, m.Key)
	assert.NotEmpty(t, m.Description)
}

func TestOperations(t *testing.T) {
	ops := Operations()
	assert.Len(t, ops, 2)
	keys := make([]string, len(ops))
	for i, op := range ops {
		keys[i] = op.Key
	}
	assert.ElementsMatch(t, []string{"query", "explain"}, keys)
}
