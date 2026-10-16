package handlers

import (
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
)

// action=test reports the masked ErrorMessage, never the raw error that can
// still carry a value the op asked to mask.
func TestMaskedExecErrorPrefersMaskedMessage(t *testing.T) {
	raw := errors.New("upstream rejected s3cr3t")
	if got := maskedExecError(&connectors.ExecuteResult{ErrorMessage: "upstream rejected wick_enc_x"}, raw); got != "upstream rejected wick_enc_x" {
		t.Fatalf("got %q, want the masked message", got)
	}
	if got := maskedExecError(nil, raw); got != raw.Error() {
		t.Fatalf("no result: got %q", got)
	}
	if got := maskedExecError(&connectors.ExecuteResult{}, raw); got != raw.Error() {
		t.Fatalf("empty message: got %q", got)
	}
}
