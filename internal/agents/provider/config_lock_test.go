package provider

import (
	"fmt"
	"sync"
	"testing"

	"github.com/yogasw/wick/internal/userconfig"
)

// Status writes (the background rescan every Save starts) load and write
// the whole config file. Racing an env save, one that loaded first used to
// write the old env back: the rows saved on the provider page vanished.
func TestStatusWritesKeepConcurrentEnvSaves(t *testing.T) {
	isolateConfig(t)
	saveSeed(t, Instance{Type: TypeClaude, Name: "waba"})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					saveOne(TypeClaude, "waba", userconfig.ProviderStatus{})
				}
			}
		}()
	}

	const n = 30
	for i := 0; i < n; i++ {
		ins, err := Find(TypeClaude, "waba")
		if err != nil {
			t.Fatal(err)
		}
		ins.Env = []string{fmt.Sprintf("CLAUDE_CONFIG_DIR=/dir-%d", i)}
		if err := Save(ins); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	waitBackgroundRescans()

	invalidateInstanceCache()
	got, err := Find(TypeClaude, "waba")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("CLAUDE_CONFIG_DIR=/dir-%d", n-1)
	if len(got.Env) != 1 || got.Env[0] != want {
		t.Fatalf("env after racing status writes = %v, want [%s]", got.Env, want)
	}
}
