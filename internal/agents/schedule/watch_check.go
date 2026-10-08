package schedule

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// A check step answers "is it done yet?" from the previous step's JSON
// without anybody writing a script — the Bitbucket pipeline / Jenkins build
// case. It reads dot paths ("state.result.name", "steps.0.name") and tests
// them with rules:
//
//	rules      met (all, or any with match=any) → matched, outcome "success"
//	fail_rules met (any)                         → matched, outcome "fail"
//	neither                                      → pending
//
// A path that does not exist simply does not match. Input that is not JSON
// at all is an error. With extract, the result handed to the session is a
// small JSON of the extracted fields plus outcome, not the whole response.

// Check ops.
const (
	OpEquals      = "equals"
	OpNotEquals   = "not_equals"
	OpIn          = "in"
	OpNotIn       = "not_in"
	OpContains    = "contains"
	OpNotContains = "not_contains"
	OpRegex       = "regex"
	OpExists      = "exists"
	OpNotExists   = "not_exists"
	OpGt          = "gt"
	OpLt          = "lt"
)

var checkOps = map[string]bool{
	OpEquals: true, OpNotEquals: true, OpIn: true, OpNotIn: true, OpContains: true, OpNotContains: true,
	OpRegex: true, OpExists: true, OpNotExists: true, OpGt: true, OpLt: true,
}

// Check outcomes, stored on a matched run.
const (
	OutcomeSuccess = "success"
	OutcomeFail    = "fail"
)

const (
	watchMaxRules    = 32
	watchMaxRegexLen = 256
	watchMaxExtract  = 32
)

// Rule is one condition of a check step.
type Rule struct {
	Path  string `json:"path"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

// String renders a rule for people: `state.name in [COMPLETED]`.
func (r Rule) String() string {
	if r.Op == OpExists || r.Op == OpNotExists {
		return r.Path + " " + r.Op
	}
	b, _ := json.Marshal(r.Value)
	return r.Path + " " + r.Op + " " + strings.ReplaceAll(string(b), `"`, "")
}

func validateCheck(s *Step, at string) error {
	if s.Script != "" || s.ToolID != "" {
		return fmt.Errorf("%s: a check step takes rules / fail_rules / match / extract only", at)
	}
	s.Match = strings.ToLower(strings.TrimSpace(s.Match))
	if s.Match != "" && s.Match != "all" && s.Match != "any" {
		return fmt.Errorf("%s.match: %q; use all (default) or any", at, s.Match)
	}
	if len(s.Rules)+len(s.FailRules) == 0 {
		return fmt.Errorf(`%s.rules: a check step needs rules and/or fail_rules, e.g. [{"path":"state.name","op":"equals","value":"COMPLETED"}]`, at)
	}
	if len(s.Rules)+len(s.FailRules) > watchMaxRules {
		return fmt.Errorf("%s.rules: too many rules (%d, max %d)", at, len(s.Rules)+len(s.FailRules), watchMaxRules)
	}
	if len(s.Extract) > watchMaxExtract {
		return fmt.Errorf("%s.extract: too many fields (max %d)", at, watchMaxExtract)
	}
	for _, l := range []struct {
		name  string
		rules []Rule
	}{{"rules", s.Rules}, {"fail_rules", s.FailRules}} {
		for i := range l.rules {
			if err := validateRule(&l.rules[i], fmt.Sprintf("%s.%s[%d]", at, l.name, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkOpList is the accepted ops, for error messages.
const checkOpList = "equals, not_equals, in, not_in, contains, not_contains, regex, exists, not_exists, gt, lt"

func validateRule(r *Rule, at string) error {
	r.Op = strings.ToLower(strings.TrimSpace(r.Op))
	if strings.TrimSpace(r.Path) == "" {
		return fmt.Errorf(`%s.path: required — a dot path into the previous step's JSON, e.g. "state.name" or "steps.0.status"`, at)
	}
	if !checkOps[r.Op] {
		return fmt.Errorf("%s.op: %q is not an op; use one of: %s", at, r.Op, checkOpList)
	}
	switch r.Op {
	case OpIn, OpNotIn:
		if _, ok := r.Value.([]any); !ok {
			return fmt.Errorf(`%s.value: op %s needs an array, e.g. ["COMPLETED","PAUSED"]`, at, r.Op)
		}
	case OpRegex:
		pat, ok := r.Value.(string)
		if !ok || pat == "" || len(pat) > watchMaxRegexLen {
			return fmt.Errorf("%s.value: op regex needs a pattern string of 1-%d chars, e.g. \"^v1\\\\.\"", at, watchMaxRegexLen)
		}
		// RE2: linear time, so a stored pattern cannot ReDoS the host.
		if _, err := regexp.Compile(pat); err != nil {
			return fmt.Errorf("%s.value: invalid regex: %v", at, err)
		}
	case OpGt, OpLt:
		if _, ok := toNumber(r.Value); !ok {
			return fmt.Errorf("%s.value: op %s needs a number, e.g. 3", at, r.Op)
		}
	}
	return nil
}

// lookupPath walks a dot path through decoded JSON. Numeric segments index
// arrays.
func lookupPath(v any, path string) (any, bool) {
	cur := v
	for _, seg := range strings.Split(path, ".") {
		switch x := cur.(type) {
		case map[string]any:
			next, ok := x[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			cur = x[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func toNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

// sameValue compares a JSON value with a rule value loosely: numbers by
// value, everything else by its string form (so "42" equals 42 and true
// equals "true").
func sameValue(a, b any) bool {
	if fa, ok := toNumber(a); ok {
		if fb, ok := toNumber(b); ok {
			return fa == fb
		}
	}
	return scalarString(a) == scalarString(b)
}

func scalarString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func evalRule(doc any, r Rule) bool {
	got, found := lookupPath(doc, r.Path)
	switch r.Op {
	case OpExists:
		return found && got != nil
	case OpNotExists:
		return !found || got == nil
	}
	if !found {
		return false // a missing path never matches — it is "not yet", not an error
	}
	switch r.Op {
	case OpEquals:
		return sameValue(got, r.Value)
	case OpNotEquals:
		return !sameValue(got, r.Value)
	case OpIn, OpNotIn:
		in := false
		list, _ := r.Value.([]any)
		for _, v := range list {
			if sameValue(got, v) {
				in = true
				break
			}
		}
		return in == (r.Op == OpIn)
	case OpContains, OpNotContains:
		has := false
		switch x := got.(type) {
		case string:
			has = strings.Contains(x, scalarString(r.Value))
		case []any:
			for _, v := range x {
				if sameValue(v, r.Value) {
					has = true
					break
				}
			}
		}
		return has == (r.Op == OpContains)
	case OpRegex:
		re, err := regexp.Compile(scalarString(r.Value))
		return err == nil && re.MatchString(scalarString(got))
	case OpGt, OpLt:
		a, ok1 := toNumber(got)
		b, ok2 := toNumber(r.Value)
		if !ok1 || !ok2 {
			return false
		}
		if r.Op == OpGt {
			return a > b
		}
		return a < b
	}
	return false
}

// evalCheck runs a check step over the previous step's output. It returns
// the run result (matched / pending), the outcome on a match, and the output
// to hand on: the extracted JSON when extract is set, else the input.
func evalCheck(s Step, input []byte) (result, outcome string, out []byte, err error) {
	var doc any
	dec := json.NewDecoder(strings.NewReader(string(input)))
	dec.UseNumber()
	if derr := dec.Decode(&doc); derr != nil {
		return entity.WatchResultError, "", nil, fmt.Errorf("check input is not JSON: %w", derr)
	}
	met := func(rules []Rule, any bool) bool {
		if len(rules) == 0 {
			return false
		}
		for _, r := range rules {
			ok := evalRule(doc, r)
			if any && ok {
				return true
			}
			if !any && !ok {
				return false
			}
		}
		return !any
	}
	switch {
	case met(s.FailRules, true):
		result, outcome = entity.WatchResultMatched, OutcomeFail
	case met(s.Rules, s.Match == "any"):
		result, outcome = entity.WatchResultMatched, OutcomeSuccess
	default:
		result = entity.WatchResultPending
	}
	if len(s.Extract) == 0 {
		return result, outcome, input, nil
	}
	ex := map[string]any{}
	for name, path := range s.Extract {
		if v, ok := lookupPath(doc, path); ok {
			ex[name] = v
		} else {
			ex[name] = nil
		}
	}
	if outcome != "" {
		ex["outcome"] = outcome
	}
	b, _ := json.Marshal(ex)
	return result, outcome, b, nil
}

// RuleResult is one rule's verdict on one run: what it wanted, what the
// input actually held there, and whether it held. Fail marks a fail_rule.
type RuleResult struct {
	Path  string `json:"path"`
	Op    string `json:"op"`
	Want  any    `json:"want,omitempty"`
	Got   any    `json:"got"`
	Found bool   `json:"found"`
	OK    bool   `json:"ok"`
	Fail  bool   `json:"fail,omitempty"`
}

// checkRuleResults evaluates every rule and fail_rule of s against input for
// the run history. Values are redacted: a rule may point at a token field.
func checkRuleResults(s Step, input []byte) []RuleResult {
	var doc any
	dec := json.NewDecoder(strings.NewReader(string(input)))
	dec.UseNumber()
	if dec.Decode(&doc) != nil {
		return nil
	}
	var out []RuleResult
	add := func(rules []Rule, fail bool) {
		for _, r := range rules {
			got, found := lookupPath(doc, r.Path)
			// Only scalars are worth echoing; a whole object would bloat the record.
			switch got.(type) {
			case map[string]any, []any:
				b, _ := json.Marshal(got)
				if len(b) > 200 {
					got = string(b[:200]) + "…"
				}
			}
			out = append(out, RuleResult{Path: r.Path, Op: r.Op, Want: r.Value, Got: redactValue(got), Found: found,
				OK: evalRule(doc, r), Fail: fail})
		}
	}
	add(s.Rules, false)
	add(s.FailRules, true)
	return out
}

// String renders a verdict for a reason line, as the value seen plus what
// the rule waits for: `state.name = IN_PROGRESS (menunggu COMPLETED)`.
func (r RuleResult) String() string {
	got := r.Path + " missing"
	if r.Found {
		got = r.Path + " = " + scalarString(r.Got)
	}
	var want string
	switch r.Op {
	case OpEquals:
		want = scalarString(r.Want)
	case OpIn:
		if list, ok := r.Want.([]any); ok {
			parts := make([]string, len(list))
			for i, v := range list {
				parts[i] = scalarString(v)
			}
			want = strings.Join(parts, " / ")
		}
	case OpExists, OpNotExists:
		want = r.Op
	default:
		want = Rule{Path: r.Path, Op: r.Op, Value: r.Want}.String()
		want = strings.TrimPrefix(want, r.Path+" ")
	}
	if r.OK {
		return got
	}
	return got + " (menunggu " + want + ")"
}
