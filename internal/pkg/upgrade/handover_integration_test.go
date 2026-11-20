//go:build linux

package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	reloadFixtureEnv = "WICK_TEST_RELOAD_FIXTURE"
	reloadDirEnv     = "WICK_TEST_RELOAD_DIR"
	reloadAddrEnv    = "WICK_TEST_RELOAD_ADDR"
)

type reloadFixtureState struct {
	PID       int  `json:"pid"`
	Inherited bool `json:"inherited"`
}

// TestRepeatedTableflipHandover runs an isolated HTTP daemon using Wick's
// actual Upgrader, then hands it over three times. It verifies that each
// successor replaces its predecessor while the inherited listener stays
// healthy. It never signals the live wick-agent process.
func TestRepeatedTableflipHandover(t *testing.T) {
	if os.Getenv(reloadFixtureEnv) == "1" {
		runReloadFixture(t)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := reservation.Addr().String()
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(stateDir, "reload-fixture.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = logFile.Sync()
		_ = logFile.Close()
		if b, err := os.ReadFile(logPath); err == nil {
			t.Logf("reload fixture log:\n%s", b)
		}
	}()

	cmd := exec.Command(exe, "-test.run=^TestRepeatedTableflipHandover$")
	cmd.Env = append(os.Environ(),
		reloadFixtureEnv+"=1",
		reloadDirEnv+"="+stateDir,
		reloadAddrEnv+"="+addr,
		"WICK_GRACEFUL_UPGRADE=1",
	)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	parentDone := make(chan error, 1)
	go func() { parentDone <- cmd.Wait() }()

	client := &http.Client{
		Timeout:   500 * time.Millisecond,
		Transport: &http.Transport{Proxy: nil},
	}
	active := map[int]struct{}{cmd.Process.Pid: {}}
	defer func() {
		if state, err := getReloadFixtureState(client, addr); err == nil {
			active[state.PID] = struct{}{}
		}
		for pid := range active {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
		for pid := range active {
			if waitPIDExit(pid, 2*time.Second) != nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}()

	current, err := waitReloadFixture(client, addr, 15*time.Second, func(s reloadFixtureState) bool { return s.PID > 0 })
	if err != nil {
		t.Fatalf("initial fixture did not serve: %v", err)
	}
	active[current.PID] = struct{}{}
	if current.Inherited {
		t.Fatalf("initial PID %d unexpectedly inherited a listener", current.PID)
	}

	for cycle := 1; cycle <= 3; cycle++ {
		oldPID := current.PID
		stopProbe := make(chan struct{})
		var probeDone sync.WaitGroup
		var probeErrors atomic.Int64
		probeDone.Add(1)
		go probeReloadFixture(client, addr, stopProbe, &probeErrors, &probeDone)

		if err := syscall.Kill(oldPID, syscall.SIGHUP); err != nil {
			close(stopProbe)
			probeDone.Wait()
			t.Fatalf("cycle %d: SIGHUP to PID %d: %v", cycle, oldPID, err)
		}
		next, err := waitReloadFixture(client, addr, 20*time.Second, func(s reloadFixtureState) bool {
			return s.PID != oldPID && s.Inherited
		})
		close(stopProbe)
		probeDone.Wait()
		if err != nil {
			t.Fatalf("cycle %d: successor did not take over from PID %d: %v", cycle, oldPID, err)
		}
		active[next.PID] = struct{}{}
		if n := probeErrors.Load(); n != 0 {
			t.Fatalf("cycle %d: HTTP probe failed %d times during handover", cycle, n)
		}

		if cycle == 1 {
			select {
			case err := <-parentDone:
				if err != nil {
					t.Fatalf("predecessor PID %d exited with error: %v", oldPID, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("predecessor PID %d did not exit after handover", oldPID)
			}
		} else if err := waitPIDExit(oldPID, 5*time.Second); err != nil {
			t.Fatalf("predecessor PID %d did not exit after handover: %v", oldPID, err)
		}
		delete(active, oldPID)
		t.Logf("cycle %d: PID %d -> %d; inherited listener healthy", cycle, oldPID, next.PID)
		current = next
	}

	// Simulate an external hard kill only in this disposable fixture. SIGKILL
	// cannot run Wick's shutdown handler, so no in-process "signal-exit" line
	// should appear; tableflip is a handover mechanism, not a supervisor.
	killedPID := current.PID
	if err := syscall.Kill(killedPID, syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL fixture PID %d: %v", killedPID, err)
	}
	if err := waitPIDExit(killedPID, 5*time.Second); err != nil {
		t.Fatalf("fixture PID %d did not exit after SIGKILL: %v", killedPID, err)
	}
	delete(active, killedPID)
	if _, err := getReloadFixtureState(client, addr); err == nil {
		t.Fatalf("HTTP still responded after SIGKILL of PID %d; tableflip unexpectedly restarted it", killedPID)
	}
	if err := logFile.Sync(); err != nil {
		t.Fatal(err)
	}
	logs, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logs), fmt.Sprintf("EVENT signal-exit pid=%d", killedPID)) {
		t.Fatalf("SIGKILL unexpectedly logged a handled signal for PID %d", killedPID)
	}
	t.Logf("SIGKILL simulation: PID %d disappeared without a signal-exit log; tableflip did not restart it", killedPID)
}

func runReloadFixture(t *testing.T) {
	baseDir, addr := os.Getenv(reloadDirEnv), os.Getenv(reloadAddrEnv)
	if baseDir == "" || addr == "" {
		t.Fatal("reload fixture requires an isolated data directory and address")
	}
	upgrader, reason := New(baseDir)
	if !upgrader.Enabled() {
		t.Fatalf("graceful upgrade unavailable: %s", reason)
	}
	defer upgrader.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	upgrader.WatchSignal(ctx)
	listener, err := upgrader.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	state := reloadFixtureState{PID: os.Getpid(), Inherited: upgrader.IsChild()}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
	})}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	upgrader.NotifyServing()
	MarkServing(state.Inherited)
	upgrader.Ready()
	RecordSuccessorReady(state.Inherited)
	fmt.Printf("EVENT serving pid=%d inherited=%t\n", state.PID, state.Inherited)

	select {
	case <-upgrader.Exit():
		fmt.Printf("EVENT handover-exit pid=%d\n", state.PID)
	case <-ctx.Done():
		fmt.Printf("EVENT signal-exit pid=%d\n", state.PID)
	case err := <-serveDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("HTTP serve: %v", err)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("HTTP shutdown: %v", err)
	}
	if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("HTTP serve after shutdown: %v", err)
	}
}

func getReloadFixtureState(client *http.Client, addr string) (reloadFixtureState, error) {
	var state reloadFixtureState
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		return state, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return state, fmt.Errorf("health status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return state, err
	}
	return state, nil
}

func waitReloadFixture(client *http.Client, addr string, timeout time.Duration, match func(reloadFixtureState) bool) (reloadFixtureState, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		state, err := getReloadFixtureState(client, addr)
		if err == nil && match(state) {
			return state, nil
		}
		if err != nil {
			lastErr = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no matching serving PID before timeout")
	}
	return reloadFixtureState{}, lastErr
}

func probeReloadFixture(client *http.Client, addr string, stop <-chan struct{}, failures *atomic.Int64, done *sync.WaitGroup) {
	defer done.Done()
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if _, err := getReloadFixtureState(client, addr); err != nil {
				failures.Add(1)
			}
		}
	}
}

func waitPIDExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if processExited(pid) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("PID %d is still present", pid)
}

func processExited(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	raw := string(b)
	closeParen := strings.LastIndexByte(raw, ')')
	if closeParen < 0 {
		return false
	}
	fields := strings.Fields(raw[closeParen+1:])
	return len(fields) > 0 && fields[0] == "Z"
}
