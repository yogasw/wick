package view

import "github.com/yogasw/wick/internal/entity"

// The "More" group, and why the active item is lifted out of it.
//
// It is a collapsed <details>, so landing on a page inside it used to
// leave the user with no idea where they were: the group shut itself
// again on navigation and the highlighted row went with it. Auto-opening
// the whole group instead was worse in the other direction — nine rows
// pushed the projects list off the screen every time.
//
// So the active item is PROMOTED: rendered above the summary as an
// ordinary nav row, and dropped from the list inside. The sidebar then
// shows exactly one extra row — the page you are on — and "More" stays
// the short, closed thing it is meant to be.

// AgentsNavItem is one row in the More group.
type AgentsNavItem struct {
	Href   string
	Label  string
	Active bool
}

// AgentsMoreItems returns the More group's rows in display order,
// already filtered by what this user may see.
//
// Ordered by use-frequency: monitoring + everyday tools first,
// config/infra last.
func AgentsMoreItems(vm AgentsLayoutVM, user *entity.User) []AgentsNavItem {
	signedIn := user != nil
	isAdmin := user != nil && user.IsAdmin()

	type candidate struct {
		path, label string
		show        bool
	}
	candidates := []candidate{
		{"/scheduled", "Scheduled", signedIn},
		// Admin-only to match its API, which is admin-gated: the page
		// reports machine-wide process usage.
		{"/resources", "Resources", isAdmin},
		{"/channels", "Channels", signedIn},
		{"/skills", "Skills", true},
		{"/agent-profiles", "Sub-agents", true},
		{"/presets", "Presets", true},
		// The Providers menu belongs to whoever looks after the provider
		// ACCOUNTS: admins, plus anyone given a manage tag on an
		// instance. Everyone else never sees it — they only ever pick a
		// provider, which the access tags gate elsewhere.
		{"/providers", "Providers", vm.ProvidersVisible},
		{"/data-tables", "Data Tables", true},
		{"/airouter", "AI Router", vm.AirouterVisible},
	}

	out := make([]AgentsNavItem, 0, len(candidates))
	for _, c := range candidates {
		if !c.show {
			continue
		}
		out = append(out, AgentsNavItem{
			Href:   vm.Base + c.path,
			Label:  c.label,
			Active: vm.ActivePage == activePageFor(c.path),
		})
	}
	return out
}

// activePageFor maps a nav path to the ActivePage key the handlers set.
func activePageFor(path string) string {
	return path[1:] // "/resources" → "resources"
}
