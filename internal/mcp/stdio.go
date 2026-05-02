package mcp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/rs/zerolog/log"
)

// ServeStdio runs the MCP JSON-RPC server over r/w for local clients
// (Claude Desktop, Cursor, etc.). Each line from r is one JSON-RPC
// message; the response is written as one JSON line to w.
//
// No auth middleware — the caller pre-populates ctx with
// login.WithUser (typically a synthetic local admin) before calling.
// Logs go to stderr so the protocol stream stays clean.
func (h *Handler) ServeStdio(ctx context.Context, r io.Reader, w io.Writer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		resp := bytes.TrimRight(h.dispatchLine(ctx, line), "\n")
		if len(resp) > 0 {
			w.Write(resp)
			w.Write([]byte("\n"))
		}
	}
	if err := scanner.Err(); err != nil {
		log.Error().Err(err).Msg("mcp stdio: scan error")
	}
}

// ServeStdioOS is the process-level entrypoint: reads os.Stdin, writes
// os.Stdout. Thin wrapper around ServeStdio for production use.
func (h *Handler) ServeStdioOS(ctx context.Context) {
	fmt.Fprintln(os.Stderr, "wick MCP server ready (stdio)")
	h.ServeStdio(ctx, os.Stdin, os.Stdout)
}

// dispatchLine processes one raw JSON-RPC message and returns the
// JSON-encoded response. It reuses ServeHTTP by creating a synthetic
// in-memory HTTP request — the context carries the pre-resolved user.
func (h *Handler) dispatchLine(ctx context.Context, body []byte) []byte {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/mcp", bytes.NewReader(body))
	if err != nil {
		return mustMarshalError("internal error")
	}
	r.Header.Set("Content-Type", "application/json")
	rw := &lineResponseWriter{header: make(http.Header)}
	h.ServeHTTP(rw, r)
	return rw.buf.Bytes()
}

func mustMarshalError(msg string) []byte {
	return []byte(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"` + msg + `"}}`)
}

// lineResponseWriter is a minimal http.ResponseWriter backed by a
// bytes.Buffer. Used by dispatchLine to capture the handler output.
type lineResponseWriter struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func (w *lineResponseWriter) Header() http.Header        { return w.header }
func (w *lineResponseWriter) WriteHeader(status int)     { w.status = status }
func (w *lineResponseWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
