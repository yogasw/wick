package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// notifyRun publishes one durable completion event from the detached wrapper.
// A watch can subscribe to source_deploy:run/<id> and wake the LLM immediately,
// without polling the status operation for the whole build.
func notifyRun() int {
	id := strings.TrimSpace(os.Getenv("WICK_DEPLOY_RUN_ID"))
	exitFile := strings.TrimSpace(os.Getenv("WICK_DEPLOY_EXIT"))
	if id == "" || exitFile == "" {
		fmt.Fprintln(os.Stderr, "source_deploy callback: missing run id or exit path")
		return 2
	}
	b, err := os.ReadFile(exitFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "source_deploy callback:", err)
		return 1
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		code = -1
	}
	state := StateSucceeded
	if code != 0 {
		state = StateFailed
	}
	if err := publishRun(id, exitFile, state, code); err != nil {
		fmt.Fprintln(os.Stderr, "source_deploy callback:", err)
		return 1
	}
	return 0
}

// publishRun sends a finished run's event through the host API in this
// process's environment and marks it delivered.
func publishRun(id, exitFile, state string, code int) error {
	tail, stage := readTail(strings.TrimSuffix(exitFile, ".exit")+".log", 40)
	args, _ := json.Marshal(map[string]any{
		"topic": "run/" + id,
		"payload": map[string]any{
			"run_id": id, "state": state, "exit_code": code,
			"stage": stage, "tail": tail,
		},
	})
	h, err := host.FromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.Invoke(ctx, &hostpb.InvokeRequest{Method: "events.publish", ArgsJson: args}); err != nil {
		return err
	}
	return os.WriteFile(notifiedPath(exitFile), nil, 0o600)
}

func notifiedPath(exitFile string) string { return strings.TrimSuffix(exitFile, ".exit") + ".notified" }

// notifyPendingWindow is how recent a finished run must be for a later
// plugin process to still deliver its event.
const notifyPendingWindow = time.Hour

// FlushNotifications delivers completion events the wrapper could not.
// The wrapper inherits the host API address of the wick that started the
// run; a deploy that hands over to a new wick retires that address, so its
// own callback is refused. The plugin process the new wick runs has the
// current address and sends what is still pending. Safe to call often:
// a run is published once, and only within notifyPendingWindow.
func (s *Service) FlushNotifications() {
	if !s.flushing.CompareAndSwap(false, true) {
		return
	}
	defer s.flushing.Store(false)
	ents, err := os.ReadDir(s.runsDir())
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".exit") {
			continue
		}
		exitFile := filepath.Join(s.runsDir(), name)
		if _, err := os.Stat(notifiedPath(exitFile)); err == nil {
			continue
		}
		fi, err := e.Info()
		if err != nil || time.Since(fi.ModTime()) > notifyPendingWindow {
			continue
		}
		b, err := os.ReadFile(exitFile)
		if err != nil {
			continue
		}
		code, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			code = -1
		}
		state := StateSucceeded
		if code != 0 {
			state = StateFailed
		}
		_ = s.publish(strings.TrimSuffix(name, ".exit"), exitFile, state, code)
	}
}
