package connectors

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const fileTestSecret = "s3cr3t-token-value-42"

// fileOpsModule returns a binary file the way Slack read_file does (raw
// Data) and a CSV export carrying a value the op asked wick to mask.
func fileOpsModule(blob []byte) connector.Module {
	return connector.Module{
		Meta: connector.Meta{Key: "files", Name: "Files", Description: "test", Fixed: true},
		Operations: []connector.Category{
			connector.Cat("", "",
				connector.Op("bin", "Bin", "binary file", struct{}{},
					func(c *connector.Ctx) (any, error) {
						return connector.WithFile(map[string]any{"file_id": "F1", "size": len(blob)},
							connector.FileOutput{Name: "clip.mp4", Format: "mp4", Data: blob}), nil
					}, wickdocs.Docs{}),
				connector.Op("csv", "CSV", "text file", struct{}{},
					func(c *connector.Ctx) (any, error) {
						c.Mask("", []string{fileTestSecret})
						return connector.WithFile(map[string]any{"rows": 1},
							connector.FileOutput{Name: "rows", Format: "csv", Content: "id,token\n1," + fileTestSecret + "\n"}), nil
					}, wickdocs.Docs{}),
			),
		},
	}
}

// Service.Execute end to end: the binary file lands on disk byte for byte,
// and neither the agent's result nor the run log carries its content; a
// text file is masked inside the file.
func TestExecuteFileOutputToDisk(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	cwd := artifactSession(t)
	blob := make([]byte, 3<<20)
	_, _ = rand.Read(blob)
	enc64 := base64.StdEncoding.EncodeToString(blob)

	encSvc, err := enc.New(testEncKey)
	require.NoError(t, err)
	db := newSQLite(t)
	cfgs := configs.NewService(db)
	require.NoError(t, cfgs.Bootstrap(context.Background()))
	svc := NewServiceFromDB(db)
	svc.SetEnc(encSvc)
	svc.SetConfigs(cfgs)
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{fileOpsModule(blob)}))
	rows, _ := svc.List(context.Background())
	require.NotEmpty(t, rows)

	run := func(op string) *ExecuteResult {
		t.Helper()
		res, err := svc.Execute(context.Background(), ExecuteParams{
			ConnectorID: rows[0].ID, OperationKey: op, SessionID: "sess-1",
			Source: entity.ConnectorRunSourceTest, UserID: "u",
		})
		require.NoError(t, err)
		require.Equal(t, entity.ConnectorRunStatusSuccess, res.Status)
		return res
	}

	res := run("bin")
	require.Less(t, len(res.ResponseJSON), 1024)
	require.NotContains(t, res.ResponseJSON, enc64[:64])
	require.NotContains(t, res.ResponseJSON, "content_base64")
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.ResponseJSON), &got))
	ref := got[connector.FileRefKey].(map[string]any)
	require.Equal(t, "artifacts/clip.mp4", ref["path"])
	onDisk, err := os.ReadFile(filepath.Join(cwd, "artifacts", "clip.mp4"))
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(blob), sha256.Sum256(onDisk))

	row, err := svc.GetRun(context.Background(), res.RunID)
	require.NoError(t, err)
	require.NotContains(t, row.ResponseJSON, enc64[:64], "run log holds the path only")
	require.Contains(t, row.ResponseJSON, "artifacts/clip.mp4")

	res = run("csv")
	require.NotContains(t, res.ResponseJSON, fileTestSecret)
	text, err := os.ReadFile(filepath.Join(cwd, "artifacts", "rows.csv"))
	require.NoError(t, err)
	require.False(t, bytes.Contains(text, []byte(fileTestSecret)), "secret masked inside the file: %s", text)
	require.True(t, bytes.HasPrefix(text, []byte("id,token\n1,")))
}
