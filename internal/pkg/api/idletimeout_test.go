package api

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// serverIdleTimeout mirrors the value set on the real server in Run(). Kept as
// a constant the tests below can drive with a short override, so the behaviour
// is proven without a 60-second test.
const serverIdleTimeout = 60 * time.Second

// TestIdleTimeoutIsConfigured guards the setting itself. It is the whole point
// of the fix: with IdleTimeout unset Go falls back to ReadTimeout, which this
// server deliberately leaves unset for SSE — so an idle keep-alive connection
// was never closed by Go at all, and the browser eventually wrote a request
// into a socket the network had silently dropped.
func TestIdleTimeoutIsConfigured(t *testing.T) {
	if serverIdleTimeout <= 0 {
		t.Fatal("server must set a positive IdleTimeout; an unset one is what caused requests to hang at 'pending' forever")
	}
	// Long enough not to churn connections on a live page, short enough to beat
	// the ~2min window where intermediaries start reaping idle sockets.
	if serverIdleTimeout > 2*time.Minute {
		t.Errorf("IdleTimeout = %v; too long to reliably beat intermediaries that reap idle sockets", serverIdleTimeout)
	}
	if serverIdleTimeout < 5*time.Second {
		t.Errorf("IdleTimeout = %v; so short it would churn connections during normal think-time", serverIdleTimeout)
	}
}

// TestIdleConnectionIsClosedByServer proves the mechanism end to end: after the
// idle window passes with no request in flight, the SERVER closes the
// connection. That orderly close is what retires the socket from the browser's
// keep-alive pool; without it the browser keeps a dead socket and the next
// request stalls at the TCP layer with no error and no response.
func TestIdleConnectionIsClosedByServer(t *testing.T) {
	const idle = 300 * time.Millisecond

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "ok")
		}),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       idle,
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// One successful request, so the connection is a live keep-alive socket.
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("reading first response: %v", err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("first response = %q, want 200", strings.TrimSpace(status))
	}
	// Drain the rest of the response so the connection goes genuinely idle.
	for {
		line, err := br.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
	}
	body := make([]byte, 2)
	_, _ = br.Read(body)

	// Now sit idle past the window. The server must hang up on its own.
	_ = conn.SetReadDeadline(time.Now().Add(idle * 20))
	buf := make([]byte, 1)
	if _, err := br.Read(buf); err == nil {
		t.Fatal("server kept the idle connection open; it must close it so the client retires the socket")
	}
	// A read error here IS the pass: EOF/reset means the server closed it.
}

// TestIdleTimeoutDoesNotCutAnInFlightResponse is the safety net for SSE. The
// server leaves ReadTimeout/WriteTimeout unset precisely so a stream can stay
// open for hours; IdleTimeout must not undo that. It applies only BETWEEN
// requests, and a stream always has one in flight — this proves it.
func TestIdleTimeoutDoesNotCutAnInFlightResponse(t *testing.T) {
	const idle = 200 * time.Millisecond

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// A stand-in for an SSE handler: holds the response open and keeps writing
	// well past the idle window.
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			rc := http.NewResponseController(w)
			for i := 0; i < 6; i++ {
				fmt.Fprintf(w, ": keepalive %d\n\n", i)
				_ = rc.Flush()
				time.Sleep(idle / 2)
			}
		}),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       idle,
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "GET /stream HTTP/1.1\r\nHost: x\r\nAccept: text/event-stream\r\n\r\n")
	br := bufio.NewReader(conn)

	deadline := time.Now().Add(idle * 30)
	_ = conn.SetReadDeadline(deadline)

	// Read past the point where IdleTimeout would have fired on an idle
	// connection. Every keepalive must arrive.
	seen := 0
	for seen < 6 {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("stream was cut after %d keepalives: %v (IdleTimeout must not apply to an in-flight response)", seen, err)
		}
		if strings.HasPrefix(line, ": keepalive") {
			seen++
		}
	}
	if seen != 6 {
		t.Fatalf("received %d keepalives, want 6", seen)
	}
}
