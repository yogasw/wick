package event

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// classifyCase is one row of testdata/classify_cases.json — the fixture the
// FE classifier port runs too, so both sides agree on every kind.
type classifyCase struct {
	Name      string   `json:"name"`
	Tool      string   `json:"tool"`
	Call      bool     `json:"call"`
	IsError   bool     `json:"is_error"`
	Input     string   `json:"input"`
	Kind      string   `json:"kind"`
	Mime      string   `json:"mime"`
	Lang      string   `json:"lang"`
	Command   string   `json:"command"`
	Summary   string   `json:"summary"`
	Cwd       string   `json:"cwd"`
	Path      string   `json:"path"`
	Pattern   string   `json:"pattern"`
	Connector string   `json:"connector"`
	Op        string   `json:"op"`
	Parts     []string `json:"parts"`
}

func TestClassifyCases(t *testing.T) {
	data, err := os.ReadFile("testdata/classify_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []classifyCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var d Display
			if c.Call {
				d = ClassifyCall(c.Tool, c.Input)
			} else {
				d = ClassifyResult(c.Tool, c.Input, c.IsError, nil)
			}
			if d.Kind != c.Kind {
				t.Fatalf("kind = %q, want %q (body %.80q)", d.Kind, c.Kind, d.Body)
			}
			check := func(field, got, want string) {
				if want != "" && got != want {
					t.Errorf("%s = %q, want %q", field, got, want)
				}
			}
			check("mime", d.Mime, c.Mime)
			check("lang", d.Lang, c.Lang)
			check("command", d.Command, c.Command)
			check("summary", d.Summary, c.Summary)
			check("cwd", d.Cwd, c.Cwd)
			check("path", d.Path, c.Path)
			check("pattern", d.Pattern, c.Pattern)
			check("connector", d.Connector, c.Connector)
			check("op", d.Op, c.Op)
			if len(c.Parts) > 0 {
				if len(d.Parts) != len(c.Parts) {
					t.Fatalf("parts = %d, want %d", len(d.Parts), len(c.Parts))
				}
				for i, k := range c.Parts {
					check("part kind", d.Parts[i].Kind, k)
				}
			}
			if d.OriginalBytes == 0 {
				t.Error("OriginalBytes not set")
			}
			if IsBinary(d.Kind) && (d.Body != "" || len(d.Blob) == 0) {
				t.Errorf("binary kind must carry Blob and no Body (body=%d blob=%d)", len(d.Body), len(d.Blob))
			}
		})
	}
}

func TestClassifyUnwrapsEscapedMarkdown(t *testing.T) {
	raw := `"# P1 Title\n\n- **a**\n- b"`
	d := Classify("Task", raw)
	if d.Kind != KindMarkdown || !strings.HasPrefix(d.Body, "# P1 Title\n") {
		t.Fatalf("got %q %q", d.Kind, d.Body)
	}
	if d.OriginalBytes != len(raw) {
		t.Fatalf("OriginalBytes = %d, want %d", d.OriginalBytes, len(raw))
	}
}

func TestClassifyImageChipFields(t *testing.T) {
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	call := ClassifyCall("Read", `{"file_path":"/x/shot.png"}`)
	d := ClassifyResult("Read", `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+png+`"}}]`, false, &call)
	if d.Kind != KindImage || d.Mime != "image/png" || d.Name != "shot.png" || d.OriginalBytes != len(d.Blob) {
		t.Fatalf("got %+v", d)
	}
	if d.Summary != "PNG · 70 B" {
		t.Fatalf("summary = %q", d.Summary)
	}
}

func TestRegisterDetectorPriority(t *testing.T) {
	detectorsMu.Lock()
	saved := detectors
	detectorsMu.Unlock()
	t.Cleanup(func() {
		detectorsMu.Lock()
		detectors = saved
		detectorsMu.Unlock()
	})
	RegisterDetector("csv", 45, func(in DetectInput) (Display, bool) {
		if strings.Count(in.Text, ",") >= 2 && strings.Contains(in.Text, "\n") {
			return Display{Kind: "csv", Body: in.Text}, true
		}
		return Display{}, false
	})
	if d := Classify("", "a,b,c\n1,2,3"); d.Kind != "csv" {
		t.Fatalf("new kind not picked up: %q", d.Kind)
	}
	// JSON (priority 50) still wins over the csv detector (45).
	if d := Classify("", `["a,b","c,d"]`+"\n"); d.Kind != KindJSON {
		t.Fatalf("priority not honored: %q", d.Kind)
	}
	// A tool-specific detector sees the normalized family.
	RegisterDetector("ticket", 85, func(in DetectInput) (Display, bool) {
		if in.Call && strings.HasSuffix(in.ToolName, "ticket_get") {
			return Display{Kind: "ticket"}, true
		}
		return Display{}, false
	})
	if d := ClassifyCall("mcp__wick__ticket_get", `{}`); d.Kind != "ticket" || d.Tool != ToolMCP {
		t.Fatalf("got %q / %q", d.Kind, d.Tool)
	}
}

func TestToolFamily(t *testing.T) {
	for name, want := range map[string]string{
		"Bash": ToolBash, "exec_command": ToolBash, "Shell": ToolBash, "functions.shell": ToolBash,
		"Read": ToolRead, "read": ToolRead, "MultiEdit": ToolEdit, "apply_patch": ToolPatch,
		"Grep": ToolGrep, "Glob": ToolGlob, "mcp__x__y": ToolMCP, "github.get_issue": ToolMCP,
		"wick_execute": ToolMCP, "WebSearch": ToolWebSearch, "TodoWrite": "",
	} {
		if got := ToolFamily(name); got != want {
			t.Errorf("ToolFamily(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int]string{12: "12 B", 340 * 1024: "340 KB", 14 << 20: "14.0 MB"} {
		if got := HumanBytes(n); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
