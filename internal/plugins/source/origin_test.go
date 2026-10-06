package source

import (
	"testing"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/entity"
)

// TestOriginOf: each install path records where the plugin came from.
func TestOriginOf(t *testing.T) {
	m := &Manager{DB: newDB(t)}
	for _, s := range []entity.PluginSource{
		{ID: "idx", Type: TypeURL, URL: "https://example.test/plugins.json"},
		{ID: "zip", Type: TypeURL, URL: "https://example.test/dl/echo-1.0.0-linux-amd64.ZIP?x=1"},
		{ID: "gh", Type: TypeGitHub, Owner: "acme", Repo: "plugins"},
	} {
		if err := m.DB.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]string{
		"":    connplugin.OriginUpload,
		"idx": connplugin.OriginSource,
		"zip": connplugin.OriginURLZip,
		"gh":  connplugin.OriginSource,
	} {
		if got := m.originOf(id); got != want {
			t.Errorf("originOf(%q) = %q, want %q", id, got, want)
		}
	}
}
