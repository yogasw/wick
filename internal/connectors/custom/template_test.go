package custom

import (
	"strings"
	"testing"
)

func TestRenderTemplate(t *testing.T) {
	cases := []struct {
		name    string
		tmpl    string
		cfg, in map[string]string
		want    string
		wantErr string // substring; empty = success expected
	}{
		{
			name: "cfg and in substitution",
			tmpl: "{{.cfg.base}}/items/{{.in.id}}",
			cfg:  map[string]string{"base": "https://api.example.com"},
			in:   map[string]string{"id": "7"},
			want: "https://api.example.com/items/7",
		},
		{
			name: "nil maps render static text",
			tmpl: "static",
			want: "static",
		},
		{
			name:    "missing cfg key errors instead of <no value>",
			tmpl:    "{{.cfg.nope}}",
			cfg:     map[string]string{"other": "x"},
			wantErr: "template",
		},
		{
			name:    "missing in key errors",
			tmpl:    "{{.in.absent}}",
			in:      map[string]string{},
			wantErr: "template",
		},
		{
			name: "default falls back on empty",
			tmpl: `{{default "fallback" .in.v}}`,
			in:   map[string]string{"v": ""},
			want: "fallback",
		},
		{
			name: "default keeps non-empty value",
			tmpl: `{{default "fallback" .in.v}}`,
			in:   map[string]string{"v": "set"},
			want: "set",
		},
		{
			name: "lower and upper",
			tmpl: "{{lower .in.a}}-{{upper .in.b}}",
			in:   map[string]string{"a": "ABC", "b": "def"},
			want: "abc-DEF",
		},
		{
			name: "b64 with printf for basic auth recipes",
			tmpl: `Basic {{b64 (printf "%s:%s" .cfg.user .cfg.pass)}}`,
			cfg:  map[string]string{"user": "user", "pass": "pass"},
			want: "Basic dXNlcjpwYXNz",
		},
		{
			name: "urlquery escapes",
			tmpl: "q={{urlquery .in.q}}",
			in:   map[string]string{"q": "a b&c"},
			want: "q=a+b%26c",
		},
		{
			name: "js escapes quotes",
			tmpl: `"{{js .in.s}}"`,
			in:   map[string]string{"s": `he"llo`},
			want: `"he\"llo"`,
		},
		{
			name:    "output over 1MB rejected",
			tmpl:    "{{.in.big}}",
			in:      map[string]string{"big": strings.Repeat("x", maxTemplateOutput+1)},
			wantErr: "exceeds",
		},
		{
			name:    "parse error surfaces with template name",
			tmpl:    "{{.cfg.x",
			wantErr: "url template",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderTemplate("url", tc.tmpl, tc.cfg, tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("renderTemplate: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderTemplateOutputAtCapPasses(t *testing.T) {
	in := map[string]string{"big": strings.Repeat("x", maxTemplateOutput)}
	got, err := renderTemplate("body", "{{.in.big}}", nil, in)
	if err != nil {
		t.Fatalf("exactly-at-cap output should pass: %v", err)
	}
	if len(got) != maxTemplateOutput {
		t.Errorf("len = %d, want %d", len(got), maxTemplateOutput)
	}
}
