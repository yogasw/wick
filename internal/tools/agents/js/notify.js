// Lightweight attention nudges for when the agent needs you but the
// tab isn't focused — a short beep + a browser Notification. Covers
// the "tab open in the background" case; the PWA web-push path
// (push.js + service worker) covers "web UI fully closed".
//
// window.WickNotify.alert(title, body) is called by agents.js on
// ask_user / approval_request SSE events. It no-ops when the tab is
// already focused (you're looking — no need to nag).
(function () {
  "use strict";

  var audioCtx = null;

  // Browsers gate BOTH Notification permission and audio playback behind
  // a user gesture. On the first click anywhere we (a) request
  // Notification permission and (b) create + resume the AudioContext so
  // later beeps actually play — without this unlock the beep is silently
  // dropped by the autoplay policy. Runs once.
  function unlockOnFirstGesture() {
    var once = function () {
      document.removeEventListener("click", once);
      document.removeEventListener("keydown", once);
      if ("Notification" in window && Notification.permission === "default") {
        try { Notification.requestPermission(); } catch (e) { /* older API */ }
      }
      try {
        var AC = window.AudioContext || window.webkitAudioContext;
        if (AC) {
          if (!audioCtx) audioCtx = new AC();
          if (audioCtx.state === "suspended") audioCtx.resume();
        }
      } catch (e) { /* audio unavailable */ }
    };
    document.addEventListener("click", once);
    document.addEventListener("keydown", once);
  }
  unlockOnFirstGesture();

  function beep() {
    try {
      var AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return;
      if (!audioCtx) audioCtx = new AC();
      if (audioCtx.state === "suspended") audioCtx.resume();
      // A short two-tone chime so it reads as "attention", not an error.
      var now = audioCtx.currentTime;
      [880, 1175].forEach(function (freq, i) {
        var osc = audioCtx.createOscillator();
        var gain = audioCtx.createGain();
        osc.type = "sine";
        osc.frequency.value = freq;
        var t = now + i * 0.16;
        gain.gain.setValueAtTime(0.0001, t);
        gain.gain.exponentialRampToValueAtTime(0.18, t + 0.02);
        gain.gain.exponentialRampToValueAtTime(0.0001, t + 0.14);
        osc.connect(gain);
        gain.connect(audioCtx.destination);
        osc.start(t);
        osc.stop(t + 0.15);
      });
    } catch (e) { /* audio blocked — notification still fires */ }
  }

  function focused() {
    // hasFocus() is the tightest signal; visibilityState catches
    // minimised / background tabs that still report focus on some
    // platforms.
    return document.hasFocus() && document.visibilityState === "visible";
  }

  function notify(title, body) {
    if (!("Notification" in window) || Notification.permission !== "granted") return;
    try {
      var n = new Notification(title, {
        body: body || "",
        tag: "wick-ask", // collapse repeats into one
        renotify: true,
      });
      n.onclick = function () { window.focus(); n.close(); };
    } catch (e) { /* construction can throw on some mobile browsers */ }
  }

  window.WickNotify = {
    // alert fires the beep + notification only when the tab is not the
    // user's active focus. Safe to call on every ask — it self-gates.
    alert: function (title, body) {
      if (focused()) return;
      beep();
      notify(title || "Agent needs your input", body);
    },
  };
})();
