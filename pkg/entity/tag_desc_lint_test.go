package entity

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// tagRe pulls the payload out of a `wick:"..."` struct tag, tolerating the
// escaped quotes descriptions use when they quote a format ("YYYY-MM-DD").
var tagRe = regexp.MustCompile(`wick:"((?:[^"\\]|\\.)*)"`)

// tagOptionKeys is every key parseWickTag / widgetFor actually consume. A
// segment whose key is NOT in here is prose, not an option.
var tagOptionKeys = map[string]bool{
	"default": true, "desc": true, "group": true, "hidden": true, "key": true,
	"locked": true, "mode": true, "regen": true, "required": true, "secret": true,
	"visible_when": true, "textarea": true, "dropdown": true, "html": true,
	"kvlist": true, "picker": true, "email": true, "url": true, "color": true,
	"date": true, "datetime": true, "number": true, "checkbox": true,
	"bool": true, "boolean": true,
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod above the test directory")
		}
		dir = parent
	}
}

// TestWickTagDescriptionsAreNotSplitByProse guards a failure mode that is
// invisible at the call site: parseWickTag separates tag options with `;`, so
// a `desc=` whose PROSE contains a semicolon is cut short there — the rest of
// the sentence never reaches the connector schema the model reads. Worse, when
// a stray fragment happens to read like an option name (`checkbox=true/false`,
// `date="YYYY-MM-DD"`) it silently switches on a widget nobody asked for.
//
// Nothing about the tag looks wrong when this happens, and the truncation only
// shows up by diffing a live input_schema against the source, so this walks the
// tree and fails with the exact line instead. Options that legitimately follow
// desc= (`;key=`, `;visible_when=`, `;mode=`, a bare flag) still pass — only
// prose is rejected. Fix a failure by rewriting the prose to drop the
// semicolon, not by moving the option.
func TestWickTagDescriptionsAreNotSplitByProse(t *testing.T) {
	root := repoRoot(t)
	var problems []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "vendor", "dist", "template":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for lineNo, line := range strings.Split(string(raw), "\n") {
			for _, m := range tagRe.FindAllStringSubmatch(line, -1) {
				if frag, bad := proseAfterDesc(m[1]); bad {
					problems = append(problems, rel+":"+strconv.Itoa(lineNo+1)+
						"\n      dropped from the description: "+frag)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(problems) > 0 {
		t.Fatalf("%d wick tag(s) have a description truncated by a semicolon in the prose.\n"+
			"`;` separates tag options, so everything after it is parsed as an option and lost\n"+
			"from the description. Replace the semicolon in the prose (a comma reads fine).\n\n    %s",
			len(problems), strings.Join(problems, "\n    "))
	}
}

// proseAfterDesc reports whether a tag payload's desc= value is followed by a
// segment that is prose rather than a recognised option, and returns the first
// such fragment. Once a segment is prose, everything after it is prose too — an
// option cannot follow prose.
func proseAfterDesc(payload string) (string, bool) {
	_, found, tail := partition(payload, "desc=")
	if !found {
		return "", false
	}
	segs := strings.Split(tail, ";")
	for _, seg := range segs[1:] {
		key := strings.TrimSpace(strings.SplitN(seg, "=", 2)[0])
		if !tagOptionKeys[key] || strings.Contains(key, " ") {
			return strings.TrimSpace(seg), true
		}
	}
	return "", false
}

func partition(s, sep string) (before string, found bool, after string) {
	i := strings.Index(s, sep)
	if i < 0 {
		return s, false, ""
	}
	return s[:i], true, s[i+len(sep):]
}
