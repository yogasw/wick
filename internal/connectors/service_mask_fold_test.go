package connectors

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// A value passed to c.MaskIgnoreCase is swept case-insensitively from the
// error message too: an upstream that echoes it in another case must not
// leak it.
func TestExecuteSweepsMaskIgnoreCaseInError(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	encSvc, err := enc.New(testEncKey)
	if err != nil {
		t.Fatal(err)
	}
	const value = "ada.lovelace@example.com"
	mod := connector.Module{
		Meta: connector.Meta{Key: "stub", Name: "Stub", Description: "test", Fixed: true},
		Operations: []connector.Category{connector.Cat("", "",
			connector.Op("fail", "Fail", "fail", struct{}{}, func(c *connector.Ctx) (any, error) {
				c.MaskIgnoreCase("", []string{value})
				return nil, errors.New("upstream rejected " + strings.ToUpper(value))
			}, wickdocs.Docs{}),
		)},
	}
	svc, connID := newSvcWithModule(t, encSvc, mod)
	res, execErr := svc.Execute(context.Background(), ExecuteParams{ConnectorID: connID, OperationKey: "fail",
		Source: entity.ConnectorRunSourceMCP, UserID: "user-A"})
	if execErr == nil || res == nil {
		t.Fatalf("want a failed run, got %+v, %v", res, execErr)
	}
	if strings.Contains(strings.ToLower(res.ErrorMessage), value) || !strings.Contains(res.ErrorMessage, "wick_enc_") {
		t.Fatalf("ErrorMessage not swept case-insensitively: %s", res.ErrorMessage)
	}
}
