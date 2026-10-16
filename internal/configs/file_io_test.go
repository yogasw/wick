package configs

import (
	"context"
	"testing"
)

// The file_io.* rows are seeded with their defaults, and a value the
// artifact writer would ignore (a denied or malformed format, a size out of
// range) is refused at the shared write path, leaving the stored one.
func TestFileIOSettings(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	if err := svc.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// Stored empty, so the built-in list follows upgrades.
	if got := svc.Get(KeyFileAllowedFormats); got != "" {
		t.Fatalf("default formats = %q", got)
	}
	if got := svc.Get(KeyFileMaxBinaryMB); got != "" {
		t.Fatalf("default max = %q", got)
	}

	for _, bad := range []string{"png,exe", "sh", "png,../x", "PHP", "all", "any", "png,xhtml", "desktop"} {
		if err := svc.Set(ctx, KeyFileAllowedFormats, bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if got := svc.Get(KeyFileAllowedFormats); got != "" {
		t.Fatalf("rejected write changed the value: %q", got)
	}
	if err := svc.Set(ctx, KeyFileAllowedFormats, "png,pdf,html"); err != nil {
		t.Fatalf("valid list refused: %v", err)
	}
	if got := svc.Get(KeyFileAllowedFormats); got != "png,pdf,html" {
		t.Fatalf("value not applied: %q", got)
	}

	for _, bad := range []string{"0", "-1", "201", "lots"} {
		if err := svc.Set(ctx, KeyFileMaxBinaryMB, bad); err == nil {
			t.Fatalf("max %q accepted", bad)
		}
	}
	if err := svc.Set(ctx, KeyFileMaxBinaryMB, "200"); err != nil {
		t.Fatalf("200 refused: %v", err)
	}
}
