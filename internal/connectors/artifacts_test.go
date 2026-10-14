package connectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/pkg/connector"
)

func TestExtractFileOutput(t *testing.T) {
	cwd := t.TempDir()
	SetArtifactDirResolver(func(id string) (string, error) {
		require.Equal(t, "sess-1", id)
		return cwd, nil
	})
	t.Cleanup(func() { SetArtifactDirResolver(nil) })

	// No file: unchanged.
	out, err := extractFileOutput(`{"rows":1}`, "sess-1")
	require.NoError(t, err)
	require.Equal(t, `{"rows":1}`, out)

	resp, _ := json.Marshal(connector.WithFile(map[string]any{"rows": 2}, connector.FileOutput{
		Name: "../../etc/apps report", Format: "csv", Content: "id,name\n1,a\n2,b\n",
	}))
	out, err = extractFileOutput(string(resp), "sess-1")
	require.NoError(t, err)
	require.NotContains(t, out, "1,a", "file content never stays in the result")

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	ref := got[connector.FileRefKey].(map[string]any)
	require.Equal(t, "artifacts/apps-report.csv", ref["path"], "name sanitized, folder created, no traversal")
	b, err := os.ReadFile(filepath.Join(cwd, "artifacts", "apps-report.csv"))
	require.NoError(t, err)
	require.Equal(t, "id,name\n1,a\n2,b\n", string(b))

	// Same name again: a suffix, never an overwrite.
	out, err = extractFileOutput(string(resp), "sess-1")
	require.NoError(t, err)
	require.True(t, strings.Contains(out, "artifacts/apps-report-2.csv"), out)

	// No session, bad format: refused.
	_, err = extractFileOutput(string(resp), "")
	require.Error(t, err)
	bad, _ := json.Marshal(connector.WithFile(nil, connector.FileOutput{Name: "x", Format: "exe", Content: "MZ"}))
	_, err = extractFileOutput(string(bad), "sess-1")
	require.Error(t, err)
}
