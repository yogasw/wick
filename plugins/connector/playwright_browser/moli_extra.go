package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// extra_args passes moli fetch flags through, so a flag moli adds is usable
// without a connector release. Only page-scoped flags pass: anything that
// reads a host file or changes network, proxy, TLS, profile or output
// settings stays under the connector's control.

// moliExtraValued take a value, moliExtraBool do not.
var moliExtraValued = map[string]bool{
	"--eval": true, "--wait-script": true, "--delay-ms": true, "--redirect-wait-ms": true,
	"--wait-response-url": true, "--wait-response-url-regex": true,
	"--wait-response-body": true, "--wait-response-body-regex": true,
	"--wait-response-json": true, "--wait-response-json-regex": true,
	"--header": true, "-H": true, "--strip-mode": true, "--document-start-script": true,
	"--timeout": true, "-t": true,
}

var moliExtraBool = map[string]bool{
	"--with-frames": true, "--with-base": true, "--disable-js": true, "--disable-css": true,
	"--disable-subframes": true, "--image": true, "--font": true, "--scrollbars": true,
	"--fail": true, "--fail-with-body": true,
}

// parseMoliExtraArgs validates a JSON array of flags. A flag not listed is
// still allowed when it is a new member of the --wait- family (a value
// follows it), unless it names a file.
func parseMoliExtraArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("extra_args must be a JSON array of strings: %w", err)
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		flag := args[i]
		name := flag
		if eq := strings.IndexByte(flag, '='); eq > 0 && strings.HasPrefix(flag, "--") {
			name = flag[:eq]
		}
		inline := name != flag
		switch {
		case moliExtraBool[name] && !inline:
			out = append(out, flag)
		case moliExtraValued[name] || (strings.HasPrefix(name, "--wait-") && !strings.Contains(name, "file") && name != "--wait-selector"):
			if inline {
				out = append(out, flag)
				continue
			}
			if i+1 >= len(args) {
				return nil, fmt.Errorf("extra_args: %s needs a value", name)
			}
			out = append(out, flag, args[i+1])
			i++
		case strings.HasPrefix(flag, "-"):
			return nil, fmt.Errorf("extra_args: %s is not allowed (only page-scoped flags pass, see the field description)", name)
		default:
			return nil, fmt.Errorf("extra_args: %q is not a flag (values go right after their flag)", flag)
		}
	}
	return out, nil
}
