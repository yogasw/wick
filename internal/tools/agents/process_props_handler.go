package agents

import (
	"net/http"
	"os"
	"strconv"

	"github.com/yogasw/wick/internal/pkg/memreport"
	"github.com/yogasw/wick/pkg/tool"
)

// processPropsResponse is one process's Properties dialog.
type processPropsResponse struct {
	memreport.Props
	// Self marks this wick server, so the dialog can say which of two
	// same-named servers (a handover in progress) is the one answering.
	Self bool `json:"self"`
}

// processPropsHandler serves GET /api/processes/props?pid=N: a fresh read
// of one process, made only when the operator opens its Properties.
func processPropsHandler(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	pid, err := strconv.Atoi(c.Query("pid"))
	if err != nil || pid <= 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "pid is required"})
		return
	}
	p, err := memreport.ReadProps(pid)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, processPropsResponse{Props: p, Self: pid == os.Getpid()})
}
