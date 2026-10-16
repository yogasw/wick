package connector

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
)

func visibleWhenModule(predicate string) Module {
	exec := func(c *Ctx) (any, error) { return nil, nil }
	op := Op("pdf", "PDF", "render pdf", struct{}{}, exec, wickdocs.Docs{})
	op.VisibleWhen = predicate
	return Module{
		Meta: Meta{Key: "browser", Name: "Browser"},
		Configs: entity.StructToConfigs(struct {
			Browser string `wick:"default=chromium"`
			Token   string `wick:"secret"`
		}{}),
		Operations: []Category{Cat("Main", "", op)},
	}
}

func TestValidateModuleVisibleWhen(t *testing.T) {
	cases := []struct {
		name, predicate, wantErr string
	}{
		{"empty", "", ""},
		{"single value", "browser:moli", ""},
		{"multi value", "browser: chromium | moli ", ""},
		{"no colon", "browser=moli", "must be <field>:<a|b|c>"},
		{"unknown field", "engine:moli", "unknown config field"},
		{"secret field", "token:abc", "secret field"},
		{"no value", "browser: | ", "lists no value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateModule(visibleWhenModule(tc.predicate))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
			if !strings.Contains(err.Error(), `"pdf"`) {
				t.Fatalf("error should name the op: %v", err)
			}
		})
	}
}

func TestEffectiveConfigValues(t *testing.T) {
	mod := visibleWhenModule("browser:moli")
	vals := EffectiveConfigValues(mod, nil)
	if vals["browser"] != "chromium" {
		t.Fatalf("default not applied: %q", vals["browser"])
	}
	vals = EffectiveConfigValues(mod, map[string]string{"browser": "moli", "extra": "x"})
	if vals["browser"] != "moli" || vals["extra"] != "x" {
		t.Fatalf("stored must win: %v", vals)
	}
	vals = EffectiveConfigValues(mod, map[string]string{"browser": ""})
	if vals["browser"] != "" {
		t.Fatalf("explicit empty must win over default: %q", vals["browser"])
	}
}

func TestOpVisible(t *testing.T) {
	mod := visibleWhenModule("browser:cloakbrowser|moli")
	op := mod.AllOps()[0]
	if OpVisible(op, EffectiveConfigValues(mod, nil)) {
		t.Fatal("default chromium must hide the op")
	}
	if !OpVisible(op, EffectiveConfigValues(mod, map[string]string{"browser": "moli"})) {
		t.Fatal("moli must show the op")
	}
	if !OpVisible(Operation{Key: "x"}, nil) {
		t.Fatal("op without VisibleWhen is always visible")
	}
}
