package wickdocs

import (
	"encoding/json"
	"testing"
)

func TestDocsIsZero(t *testing.T) {
	t.Run("empty docs reports zero", func(t *testing.T) {
		if !(Docs{}).IsZero() {
			t.Fatalf("Docs{}.IsZero() = false, want true")
		}
	})

	t.Run("any populated field flips to non-zero", func(t *testing.T) {
		cases := []struct {
			name string
			d    Docs
		}{
			{"OutputShape", Docs{OutputShape: map[string]string{"x": "y"}}},
			{"TemplateableFields", Docs{TemplateableFields: []string{"prompt"}}},
			{"Quirks", Docs{Quirks: []string{"q"}}},
			{"Examples", Docs{Examples: []Example{{Name: "n", YAML: "y"}}}},
			{"PairWith", Docs{PairWith: []string{"x"}}},
			{"CommonPitfalls", Docs{CommonPitfalls: []string{"p"}}},
			{"InputSample", Docs{InputSample: `{"k":"v"}`}},
			{"OutputSample", Docs{OutputSample: `{"ok":true}`}},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				if tc.d.IsZero() {
					t.Fatalf("Docs with %s populated reported IsZero=true", tc.name)
				}
			})
		}
	})
}

func TestDocsJSONOmitEmpty(t *testing.T) {
	// Empty Docs should marshal to "{}" so embedding it in a larger
	// response payload doesn't leak `output_shape:null`, etc.
	b, err := json.Marshal(Docs{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "{}" {
		t.Fatalf("empty Docs marshalled to %s, want {}", b)
	}
}

func TestExampleRoundTrip(t *testing.T) {
	e := Example{Name: "basic", YAML: "- id: x\n  type: agent\n"}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Example
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != e {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, e)
	}
}
