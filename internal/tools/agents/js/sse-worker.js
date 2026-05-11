// SharedWorker — persists across page navigations within the same origin.
// Holds one EventSource per sessionID; pages subscribe/unsubscribe via
// MessagePort without triggering a new HTTP connection.
//
// Message protocol (page → worker):
//   { type: "subscribe",   sessionID: "<id>", base: "<url-base>" }
//   { type: "unsubscribe", sessionID: "<id>" }
//
// Message protocol (worker → page):
//   { type: "event",  sessionID: "<id>", event: <parsed-event-object> }
//   { type: "status", sessionID: "<id>", status: "connected"|"error" }

"use strict";

// ports[sessionID] = Set of MessagePort
var ports = {};
// sources[sessionID] = EventSource
var sources = {};

self.onconnect = function (e) {
  var port = e.ports[0];

  port.onmessage = function (msg) {
    var data = msg.data;
    if (!data || !data.type) return;

    if (data.type === "subscribe") {
      var sid = data.sessionID;
      var base = data.base;
      if (!sid || !base) return;

      // Register port for this session.
      if (!ports[sid]) ports[sid] = new Set();
      ports[sid].add(port);

      // Reuse existing EventSource if already open.
      if (sources[sid] && sources[sid].readyState !== EventSource.CLOSED) {
        port.postMessage({ type: "status", sessionID: sid, status: "connected" });
        return;
      }

      // Open new EventSource.
      var url = base + "/stream?session=" + encodeURIComponent(sid);
      var es = new EventSource(url, { withCredentials: true });
      sources[sid] = es;

      es.addEventListener("agent", function (ev) {
        var parsed;
        try { parsed = JSON.parse(ev.data); } catch (_) { return; }
        broadcast(sid, { type: "event", sessionID: sid, event: parsed });
      });

      es.onopen = function () {
        broadcast(sid, { type: "status", sessionID: sid, status: "connected" });
      };

      es.onerror = function () {
        broadcast(sid, { type: "status", sessionID: sid, status: "error" });
        // EventSource auto-reconnects; we don't close it here.
      };

    } else if (data.type === "unsubscribe") {
      var sid = data.sessionID;
      if (!sid || !ports[sid]) return;
      ports[sid].delete(port);
      // If no pages left watching this session, close the EventSource.
      if (ports[sid].size === 0) {
        delete ports[sid];
        if (sources[sid]) {
          sources[sid].close();
          delete sources[sid];
        }
      }
    }
  };

  port.start();
};

function broadcast(sessionID, msg) {
  var set = ports[sessionID];
  if (!set) return;
  set.forEach(function (p) {
    try { p.postMessage(msg); } catch (_) { /* port gone */ }
  });
}
