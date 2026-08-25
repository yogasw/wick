package askuser

import (
	"sync"
	"testing"
	"time"
)

func TestAsk_HappyPath(t *testing.T) {
	requested := make(chan AskRequest, 1)
	mgr := NewManager(Options{
		DefaultTimeout: 2 * time.Second,
		OnRequest:      func(r AskRequest) { requested <- r },
	})

	ansCh := make(chan Answer, 1)
	go func() {
		ans, err := mgr.Ask(Question{
			SessionID: "S1",
			Question:  "Pakai Postgres atau MySQL?",
			Options: []Option{
				{Label: "Postgres", Value: "pg"},
				{Label: "MySQL", Value: "mysql"},
			},
		}, nil)
		if err != nil {
			t.Errorf("Ask: %v", err)
		}
		ansCh <- ans
	}()

	req := <-requested
	if req.SessionID != "S1" || req.Question == "" || req.ID == "" {
		t.Errorf("request: %+v", req)
	}

	if !mgr.Resolve(req.ID, Answer{Value: "pg"}) {
		t.Fatal("Resolve returned false")
	}

	select {
	case ans := <-ansCh:
		if ans.Value != "pg" {
			t.Errorf("Value: %q", ans.Value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not return")
	}
}

func TestAsk_Timeout(t *testing.T) {
	mgr := NewManager(Options{DefaultTimeout: 50 * time.Millisecond})
	start := time.Now()
	_, err := mgr.Ask(Question{SessionID: "S1", Question: "?"}, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestAsk_Cancel(t *testing.T) {
	mgr := NewManager(Options{DefaultTimeout: 5 * time.Second})
	done := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		_, err := mgr.Ask(Question{SessionID: "S1", Question: "?"}, done)
		errCh <- err
	}()
	close(done)
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected cancel error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not return on cancel")
	}
}

func TestAsk_ValidationErrors(t *testing.T) {
	mgr := NewManager(Options{})

	if _, err := mgr.Ask(Question{Question: "?"}, nil); err == nil {
		t.Error("expected error when SessionID empty")
	}
	if _, err := mgr.Ask(Question{SessionID: "S1"}, nil); err == nil {
		t.Error("expected error when Question empty")
	}
}

func TestResolve_UnknownID(t *testing.T) {
	mgr := NewManager(Options{})
	if mgr.Resolve("does-not-exist", Answer{Text: "hi"}) {
		t.Error("Resolve(unknown) should return false")
	}
}

func TestPendingFor_Snapshot(t *testing.T) {
	requested := make(chan struct{}, 4)
	mgr := NewManager(Options{
		DefaultTimeout: 2 * time.Second,
		OnRequest:      func(_ AskRequest) { requested <- struct{}{} },
	})

	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			_, _ = mgr.Ask(Question{
				SessionID: "S1",
				Question:  "Q",
			}, nil)
		}(i)
	}
	for i := 0; i < 2; i++ {
		<-requested
	}

	pending := mgr.PendingFor("S1")
	if len(pending) != 2 {
		t.Errorf("expected 2 pending, got %d", len(pending))
	}
	if got := mgr.PendingFor("other"); len(got) != 0 {
		t.Errorf("expected 0 pending for other session, got %d", len(got))
	}

	// Resolve both so the goroutines can exit before the test completes.
	for _, p := range pending {
		mgr.Resolve(p.ID, Answer{Text: "ok"})
	}
	wg.Wait()
}

func TestOnResolved_Fires(t *testing.T) {
	var (
		mu       sync.Mutex
		resolved []string
	)
	requested := make(chan AskRequest, 1)
	mgr := NewManager(Options{
		DefaultTimeout: 2 * time.Second,
		OnRequest:      func(r AskRequest) { requested <- r },
		OnResolved: func(sid, id string) {
			mu.Lock()
			resolved = append(resolved, sid+"/"+id)
			mu.Unlock()
		},
	})

	go func() {
		_, _ = mgr.Ask(Question{SessionID: "S1", Question: "Q"}, nil)
	}()
	req := <-requested
	mgr.Resolve(req.ID, Answer{Value: "yes"})

	// Brief wait for the deferred onResolved to run.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(resolved) != 1 || resolved[0] != "S1/"+req.ID {
		t.Errorf("onResolved: %+v", resolved)
	}
}
