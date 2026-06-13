package custom

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/entity"
)

func validDraft() *Draft {
	return &Draft{
		Key:  "petstore",
		Name: "Petstore",
		Configs: []DefField{
			{Key: "base_url", Widget: "url", Required: true},
			{Key: "token", Widget: "secret", Secret: true},
		},
		Ops: []DefOp{{
			Key:         "list_pets",
			Name:        "List Pets",
			Description: "List pets in the store.",
			Inputs:      []DefField{{Key: "limit", Widget: "number"}},
			Request:     &OpRequest{Method: "GET", URLTemplate: "{{.cfg.base_url}}/pets"},
		}},
	}
}

func TestValidateDraft(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(d *Draft)
		wantErr string // substring; empty = valid
	}{
		{name: "valid http draft", mutate: func(d *Draft) {}},
		{
			name: "valid mcp draft",
			mutate: func(d *Draft) {
				d.Ops[0].Request = nil
				d.Ops[0].MCPSource = &MCPSource{ServerID: "srv-1", ToolName: "list_pets"}
			},
		},
		{
			name:    "uppercase connector key",
			mutate:  func(d *Draft) { d.Key = "PetStore" },
			wantErr: "lowercase slug",
		},
		{
			name:    "key starting with digit",
			mutate:  func(d *Draft) { d.Key = "1pets" },
			wantErr: "lowercase slug",
		},
		{
			name:    "reserved key custom",
			mutate:  func(d *Draft) { d.Key = "custom" },
			wantErr: "reserved",
		},
		{
			name:    "missing name",
			mutate:  func(d *Draft) { d.Name = "  " },
			wantErr: "name is required",
		},
		{
			name: "duplicate config keys",
			mutate: func(d *Draft) {
				d.Configs = append(d.Configs, DefField{Key: "base_url"})
			},
			wantErr: "duplicate field key",
		},
		{
			name:    "bad config key",
			mutate:  func(d *Draft) { d.Configs[0].Key = "Base-URL" },
			wantErr: "snake_case",
		},
		{
			name:    "unsupported config widget",
			mutate:  func(d *Draft) { d.Configs[0].Widget = "kvlist" },
			wantErr: "unsupported widget",
		},
		{
			name:    "no ops",
			mutate:  func(d *Draft) { d.Ops = nil },
			wantErr: "at least one operation",
		},
		{
			name:    "op key not snake_case",
			mutate:  func(d *Draft) { d.Ops[0].Key = "ListPets" },
			wantErr: "snake_case",
		},
		{
			name: "duplicate op keys",
			mutate: func(d *Draft) {
				op2 := d.Ops[0]
				d.Ops = append(d.Ops, op2)
			},
			wantErr: "duplicate op key",
		},
		{
			name:    "op missing description",
			mutate:  func(d *Draft) { d.Ops[0].Description = "" },
			wantErr: "name and description are required",
		},
		{
			name:    "op input bad widget",
			mutate:  func(d *Draft) { d.Ops[0].Inputs[0].Widget = "picker" },
			wantErr: "unsupported widget",
		},
		{
			name: "request and mcp_source both set",
			mutate: func(d *Draft) {
				d.Ops[0].MCPSource = &MCPSource{ServerID: "s", ToolName: "t"}
			},
			wantErr: "mutually exclusive",
		},
		{
			name: "neither request nor mcp_source",
			mutate: func(d *Draft) {
				d.Ops[0].Request = nil
			},
			wantErr: "either request or mcp_source",
		},
		{
			name:    "unsupported http method",
			mutate:  func(d *Draft) { d.Ops[0].Request.Method = "TRACE" },
			wantErr: "unsupported HTTP method",
		},
		{
			name:    "missing url_template",
			mutate:  func(d *Draft) { d.Ops[0].Request.URLTemplate = "  " },
			wantErr: "url_template is required",
		},
		{
			name: "mcp_source missing tool_name",
			mutate: func(d *Draft) {
				d.Ops[0].Request = nil
				d.Ops[0].MCPSource = &MCPSource{ServerID: "srv-1"}
			},
			wantErr: "server_id and tool_name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := validDraft()
			tc.mutate(d)
			err := ValidateDraft(d)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateDraft: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateFields(t *testing.T) {
	cases := []struct {
		name    string
		fields  []DefField
		wantErr string
	}{
		{name: "empty ok", fields: nil},
		{name: "default widget ok", fields: []DefField{{Key: "plain"}}},
		{
			name: "all widgets accepted",
			fields: func() []DefField {
				widgets := []string{"text", "textarea", "dropdown", "number", "checkbox", "bool", "secret", "email", "url", "date", "datetime"}
				out := make([]DefField, 0, len(widgets))
				for i, w := range widgets {
					out = append(out, DefField{Key: "k" + string(rune('a'+i)), Widget: w})
				}
				return out
			}(),
		},
		{name: "dash in key", fields: []DefField{{Key: "has-dash"}}, wantErr: "snake_case"},
		{name: "leading digit", fields: []DefField{{Key: "1abc"}}, wantErr: "snake_case"},
		{name: "uppercase", fields: []DefField{{Key: "Upper"}}, wantErr: "snake_case"},
		{name: "empty key", fields: []DefField{{Key: ""}}, wantErr: "snake_case"},
		{
			name:    "duplicate keys",
			fields:  []DefField{{Key: "a"}, {Key: "a"}},
			wantErr: "duplicate field key",
		},
		{
			name:    "unknown widget",
			fields:  []DefField{{Key: "a", Widget: "kvlist"}},
			wantErr: "unsupported widget",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFields(tc.fields)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateFields: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseFieldsRoundTrip(t *testing.T) {
	in := []DefField{
		{Key: "base_url", Label: "Base URL", Widget: "url", Required: true, Default: "https://abc.example.com", Desc: "API base"},
		{Key: "token", Widget: "secret", Secret: true},
		{Key: "mode", Widget: "dropdown", Options: "a|b|c"},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseFields(string(raw))
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n in = %+v\nout = %+v", in, out)
	}

	empty, err := ParseFields("  ")
	if err != nil || len(empty) != 0 || empty == nil {
		t.Errorf("empty input: got %v, %v; want empty non-nil slice", empty, err)
	}
	if _, err := ParseFields("{not json"); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseOpsRoundTrip(t *testing.T) {
	in := []DefOp{
		{
			Key: "get_pet", Name: "Get Pet", Description: "Fetch one pet.",
			Inputs: []DefField{{Key: "pet_id", Required: true}},
			Request: &OpRequest{
				Method:      "GET",
				URLTemplate: "{{.cfg.base_url}}/pets/{{.in.pet_id}}",
				Headers:     map[string]string{"Accept": "application/json"},
			},
		},
		{
			Key: "delete_pet", Name: "Delete Pet", Description: "Remove a pet.",
			Destructive: true,
			Inputs:      []DefField{{Key: "pet_id", Required: true}},
			MCPSource:   &MCPSource{ServerID: "srv-1", ToolName: "delete_pet"},
		},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseOps(string(raw))
	if err != nil {
		t.Fatalf("ParseOps: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n in = %+v\nout = %+v", in, out)
	}

	empty, err := ParseOps("")
	if err != nil || len(empty) != 0 || empty == nil {
		t.Errorf("empty input: got %v, %v; want empty non-nil slice", empty, err)
	}
	if _, err := ParseOps("[broken"); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseSourceMeta(t *testing.T) {
	m := ParseSourceMeta(`{"category":"API","server_id":"srv-9"}`)
	if m.Category != "API" || m.ServerID != "srv-9" {
		t.Errorf("got %+v", m)
	}
	if m := ParseSourceMeta(""); m != (SourceMeta{}) {
		t.Errorf("empty column should yield zero value, got %+v", m)
	}
	if m := ParseSourceMeta("legacy-garbage"); m != (SourceMeta{}) {
		t.Errorf("legacy column should be tolerated, got %+v", m)
	}
}

func TestDefFieldToConfig(t *testing.T) {
	cases := []struct {
		name  string
		field DefField
		want  entity.Config
	}{
		{
			name:  "secret widget implies IsSecret",
			field: DefField{Key: "token", Widget: "secret", Default: "x", Required: true, Desc: "d"},
			want:  entity.Config{Key: "token", Value: "x", Type: "secret", IsSecret: true, Required: true, Description: "d"},
		},
		{
			name:  "empty widget defaults to text",
			field: DefField{Key: "plain"},
			want:  entity.Config{Key: "plain", Type: "text"},
		},
		{
			name:  "secret flag with non-secret widget",
			field: DefField{Key: "s", Widget: "text", Secret: true},
			want:  entity.Config{Key: "s", Type: "text", IsSecret: true},
		},
		{
			name:  "dropdown options pass through",
			field: DefField{Key: "mode", Widget: "dropdown", Options: "a|b"},
			want:  entity.Config{Key: "mode", Type: "dropdown", Options: "a|b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.field.ToConfig()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ToConfig = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestFieldsToConfigs(t *testing.T) {
	out := FieldsToConfigs([]DefField{{Key: "a"}, {Key: "b", Widget: "secret"}})
	if len(out) != 2 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].Key != "a" || out[1].Key != "b" || !out[1].IsSecret {
		t.Errorf("got %+v", out)
	}
}
