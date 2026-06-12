package provider

import (
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"
)

func TestRescanGroupDeduplicatesConcurrentCalls(t *testing.T) {
	rescanGroup = singleflight.Group{}

	var mu sync.Mutex
	var calls int
	blocker := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rescanGroup.Do("claude/test", func() (any, error) {
				mu.Lock()
				calls++
				mu.Unlock()
				<-blocker
				return nil, nil
			})
		}()
	}

	time.Sleep(20 * time.Millisecond)
	close(blocker)
	wg.Wait()

	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 1 {
		t.Fatalf("rescan fn called %d times for same key, want 1", n)
	}
}
