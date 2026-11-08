package logintty

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestOMPUsageFailureDroppedWhenBinaryChanges(t *testing.T) {
	old := ompRunner
	t.Cleanup(func() { ompRunner = old })
	bin := filepath.Join(t.TempDir(), "omp")
	if err := os.WriteFile(bin, []byte("broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + t.TempDir(), provider.AccountBinEnvKey + "=" + bin}
	invalidateOMPUsage(env)
	t.Cleanup(func() { invalidateOMPUsage(env) })

	calls := 0
	ompRunner = func(context.Context, []string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("exit status 127")
		}
		return []byte(ompUsageFixture), nil
	}
	if _, err := fetchOMPUsage(env, false); err == nil {
		t.Fatal("first listing should fail")
	}
	// Still the same binary: the failure is served from cache.
	if _, err := fetchOMPUsage(env, false); err == nil || calls != 1 {
		t.Fatalf("cached failure not served: calls=%d err=%v", calls, err)
	}
	// A download / activate puts a different file there.
	later := time.Now().Add(time.Minute)
	if err := os.WriteFile(bin, []byte("fixed"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(bin, later, later)
	if _, err := fetchOMPUsage(env, false); err != nil || calls != 2 {
		t.Fatalf("binary changed but cached failure kept: calls=%d err=%v", calls, err)
	}
}
