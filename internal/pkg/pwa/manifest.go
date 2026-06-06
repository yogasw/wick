// Package pwa hosts Progressive Web App glue — the dynamic web app
// manifest plus future hooks (service worker registration, push
// subscription endpoints). Kept separate from internal/pkg/api so
// server.go stays a wiring file and doesn't accumulate PWA logic.
package pwa

import (
	"encoding/json"
	"net/http"

	"github.com/yogasw/wick/internal/pkg/ui"
)

// ManifestHandler serves /public/manifest.json with the configured app
// name + description baked in, so downstream wick deployments install
// as their own brand instead of "wick". The agents shortcut name
// follows suit — "MyApp Agent" rather than a static fallback. Icons
// and theme colour stay shared from the embedded SVGs under
// /public/img.
//
// Reads everything from the request context. The caller is expected
// to chain the existing appNameHandler middleware before this handler
// so ui.AppNameFromContext / ui.AppDescFromContext are populated.
func ManifestHandler(w http.ResponseWriter, r *http.Request) {
	name := ui.AppNameFromContext(r.Context())
	desc := ui.AppDescFromContext(r.Context())
	if desc == "" {
		desc = name + " — agents and tools dashboard"
	}
	type icon struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose"`
	}
	type shortcut struct {
		Name        string `json:"name"`
		ShortName   string `json:"short_name"`
		Description string `json:"description,omitempty"`
		URL         string `json:"url"`
	}
	manifest := map[string]any{
		"name":             name,
		"short_name":       name,
		"description":      desc,
		"start_url":        "/",
		"scope":            "/",
		"display":          "standalone",
		// Splash / task-switcher colours. Kept neutral-dark to match the
		// app shell — the live in-app status bar is themed per-user by the
		// theme-color sync script in ui.Layout (the manifest is a static,
		// pre-auth asset so it can't know the user's selected theme).
		"background_color": "#142638",
		"theme_color":      "#142638",
		"icons": []icon{
			{Src: "/public/img/icon-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
			{Src: "/public/img/icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any"},
			{Src: "/public/img/icon-maskable-512.png", Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
			{Src: "/public/img/icon.svg", Sizes: "any", Type: "image/svg+xml", Purpose: "any"},
		},
		"shortcuts": []shortcut{
			{Name: name, ShortName: name, Description: "Open the " + name + " home", URL: "/"},
			{Name: name + " Agent", ShortName: "Agent", Description: "Jump straight to the agents dashboard", URL: "/tools/agents/"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(manifest)
}
