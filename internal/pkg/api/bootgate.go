package api

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"
)

// BootGate tracks the set of asynchronous boot steps that must finish before
// the HTTP surface is opened to users. It replaces the single bootReady bool:
// each async step Register()s itself while booting and Done()s when finished,
// and the gate only lifts once EVERY registered step is done. This makes
// "add another thing that must complete before the app is usable" a two-line
// change (Register + Done) instead of a fragile single flag that whichever
// goroutine finishes first might flip too early.
//
// All methods are safe for concurrent use. Ready() and PhaseLabel() are hot
// (hit on every gated request) so they read lock-free via atomics.
type BootGate struct {
	mu      sync.Mutex
	pending map[string]struct{} // steps not yet done; gate lifts when empty
	started bool                // at least one Register happened (guards empty-gate edge case)

	ready atomic.Bool
	phase atomic.Pointer[string]
}

// NewBootGate returns a gate with the given initial phase label key. The gate
// starts NOT ready; call Register for each async step, then Done as each
// finishes. If no step is ever registered the gate must be lifted explicitly
// with MarkReady (used when there is genuinely nothing to wait for).
func NewBootGate(initialPhase string) *BootGate {
	g := &BootGate{pending: make(map[string]struct{})}
	g.SetPhase(initialPhase)
	return g
}

// Register declares an async boot step the gate must wait for. Call it before
// the step's goroutine starts work. Logs so the boot timeline is visible.
func (g *BootGate) Register(name string) {
	g.mu.Lock()
	g.pending[name] = struct{}{}
	g.started = true
	remaining := len(g.pending)
	g.mu.Unlock()
	log.Info().Str("step", name).Int("pending", remaining).Msg("boot: step registered")
}

// Done marks a registered step finished. When the last pending step completes,
// the gate flips ready and logs that the gate has lifted. Calling Done for an
// unregistered or already-done name is a no-op (besides a debug log) so it is
// safe on every exit path of a goroutine.
func (g *BootGate) Done(name string) {
	g.mu.Lock()
	if _, ok := g.pending[name]; !ok {
		g.mu.Unlock()
		log.Debug().Str("step", name).Msg("boot: Done for unknown/finished step (ignored)")
		return
	}
	delete(g.pending, name)
	remaining := len(g.pending)
	lift := remaining == 0
	g.mu.Unlock()

	log.Info().Str("step", name).Int("pending", remaining).Msg("boot: step done")
	if lift {
		g.ready.Store(true)
		log.Info().Msg("boot: all steps done — gate lifted, app is ready")
	}
}

// MarkReady lifts the gate unconditionally. Used when no async step was
// registered at all (nothing to wait for) so the server doesn't sit behind
// the gate forever. No-op if already ready.
func (g *BootGate) MarkReady(reason string) {
	if g.ready.Swap(true) {
		return
	}
	log.Info().Str("reason", reason).Msg("boot: gate lifted (no async steps to wait for)")
}

// Ready reports whether the gate has lifted. Lock-free; hit per request.
func (g *BootGate) Ready() bool { return g.ready.Load() }

// SetPhase updates the human-readable phase key shown on the gate page.
func (g *BootGate) SetPhase(phase string) {
	p := phase
	g.phase.Store(&p)
}

// PhaseLabel returns the message for the current phase. Lock-free.
func (g *BootGate) PhaseLabel() string {
	phase := ""
	if p := g.phase.Load(); p != nil {
		phase = *p
	}
	return bootPhaseMessage(phase)
}

// bootPhaseMessages maps a phase key to the message shown on the gate page.
// Unknown / empty keys fall back to the generic starting message.
var bootPhaseMessages = map[string]string{
	"starting":       "Starting services…",
	"restoring":      "Restoring sessions and files…",
	"connecting-mcp": "Connecting MCP connectors…",
}

func bootPhaseMessage(phase string) string {
	if m, ok := bootPhaseMessages[phase]; ok {
		return m
	}
	return bootPhaseMessages["starting"]
}

// bootGateHandler short-circuits every request with a lightweight
// "Booting…" page while the async boot steps are still running
// (s.bootGate not ready). It is the OUTERMOST middleware so the gate
// applies before auth, host-allowlist, and routing — a user hitting any URL
// during the restore window sees progress instead of an empty sidebar or a
// 502/503 from a half-loaded registry.
//
// Exemptions (served normally even while booting):
//   - /health — load-balancer / k8s readiness probes must keep succeeding,
//     otherwise the pod is killed mid-restore and never finishes.
//   - /boot-status — the JSON the gate page polls to know when to reload.
//
// The page itself is self-contained (inline CSS, meta-refresh fallback +
// fetch poll) so it needs no static assets, which keeps the exemption list
// to just the two endpoints above.
func (s *Server) bootGateHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.bootGate == nil || s.bootGate.Ready() {
			next.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/health":
			next.ServeHTTP(w, r)
			return
		case "/boot-status":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(s.bootStatusJSON(false))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, bootGatePageHTML(s.bootPhaseLabel()))
	})
}

// bootPhaseLabel returns the current human-readable boot phase message.
func (s *Server) bootPhaseLabel() string {
	if s.bootGate == nil {
		return bootPhaseMessage("")
	}
	return s.bootGate.PhaseLabel()
}

// bootStatusJSON is the shape /boot-status returns. ready=false while the
// gate holds; message reflects the current phase so the page can update its
// label without a full reload.
func (s *Server) bootStatusJSON(ready bool) map[string]any {
	return map[string]any{"ready": ready, "message": s.bootPhaseLabel()}
}

// bootGatePageHTML renders the self-contained holding page shown while the
// server finishes its boot restore. initialMsg is the phase label baked in
// for the no-JS / first-paint case; the inline script then polls /boot-status
// every 1.5s to live-update the message and reload as soon as the server
// reports ready. The <meta refresh> is the no-JS fallback.
func bootGatePageHTML(initialMsg string) string {
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="3">
<title>Starting up…</title>
<style>
  :root { color-scheme: dark light; }
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center;
         font-family:Inter,system-ui,-apple-system,Segoe UI,Roboto,sans-serif;
         background:#0f1729; color:#e2e8f0; }
  .card { text-align:center; padding:2.5rem 3rem; }
  .spinner { width:40px; height:40px; margin:0 auto 1.5rem; border:3px solid rgba(148,163,184,.25);
             border-top-color:#34d399; border-radius:50%; animation:spin 1s linear infinite; }
  @keyframes spin { to { transform:rotate(360deg); } }
  h1 { font-size:1.125rem; font-weight:600; margin:0 0 .5rem; }
  p  { font-size:.875rem; color:#94a3b8; margin:0; }
</style>
</head>
<body>
  <div class="card">
    <div class="spinner"></div>
    <h1>Starting up…</h1>
    <p id="boot-msg">` + html.EscapeString(initialMsg) + `</p>
  </div>
  <script>
    (function () {
      function poll() {
        fetch('/boot-status', { cache: 'no-store' })
          .then(function (r) { return r.json(); })
          .then(function (d) {
            if (d && d.ready) { location.reload(); return; }
            if (d && d.message) {
              var el = document.getElementById('boot-msg');
              if (el) { el.textContent = d.message; }
            }
            setTimeout(poll, 1500);
          })
          .catch(function () { setTimeout(poll, 1500); });
      }
      setTimeout(poll, 1500);
    })();
  </script>
</body>
</html>`
}
