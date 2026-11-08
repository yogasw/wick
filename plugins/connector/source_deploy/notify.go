package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	tail, stage := readTail(strings.TrimSuffix(exitFile, ".exit")+".log", 40)
	payload := map[string]any{
		"run_id": id, "state": state, "exit_code": code,
		"stage": stage, "tail": tail,
	}
	args, _ := json.Marshal(map[string]any{
		"topic": "run/" + id,
		"payload": payload,
	})
	h, err := host.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "source_deploy callback:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.Invoke(ctx, &hostpb.InvokeRequest{Method: "events.publish", ArgsJson: args}); err != nil {
		fmt.Fprintln(os.Stderr, "source_deploy callback:", err)
		return 1
	}
	return 0
}
