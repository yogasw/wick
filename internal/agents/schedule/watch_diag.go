package schedule

import (
	"strconv"
	"strings"
)

// Diagnosis: every run records where it stopped and one sentence on why, so
// "why is my watch still pending / failing?" is answered by reading the
// history (action=runs → action=run) rather than by guessing.

const reasonMax = 300

// explainRun fills rec.StoppedAt and rec.Reason from the recorded steps.
// Reasons read "step N 'name': what happened", e.g.
//
//	step 2 'done?': state.name = IN_PROGRESS (menunggu COMPLETED)
//	step 1 'poll': exit 1 — still building
//	step 1 'pipeline': error 404 not found
func explainRun(rec *RunRecord, steps []Step) {
	if len(rec.Steps) == 0 {
		rec.Reason = clip(rec.Error)
		return
	}
	idx := len(rec.Steps) - 1
	sr := rec.Steps[idx]
	rec.StoppedAt = &StopPoint{Index: idx, Name: sr.Name, Kind: sr.Kind, ExitCode: sr.ExitCode, Decision: sr.Decision}
	why := ""
	switch sr.Kind {
	case StepKindBash:
		switch {
		case sr.ExitCode == -1:
			why = firstLineOf(sr.Error)
		default:
			why = "exit " + strconv.Itoa(sr.ExitCode)
			ll := lastLine(sr.Stderr)
			if ll == "" || sr.ExitCode == 0 || sr.ExitCode == 1 {
				if o := lastLine(sr.Output); o != "" {
					ll = o
				}
			}
			if ll != "" {
				why += " — " + ll
			}
		}
	case StepKindConnector:
		if sr.OK {
			why = "ok"
		} else {
			why = "error " + firstLineOf(sr.Error)
		}
	case StepKindCheck:
		switch {
		case !sr.OK:
			why = firstLineOf(sr.Error)
		case rec.Outcome == OutcomeFail:
			why = "fail rule — " + firstRule(sr.Rules, func(r RuleResult) bool { return r.Fail && r.OK })
		case rec.Outcome == OutcomeSuccess:
			why = "matched — " + firstRule(sr.Rules, func(r RuleResult) bool { return !r.Fail && r.OK })
		default:
			why = firstRule(sr.Rules, func(r RuleResult) bool { return !r.Fail && !r.OK })
		}
	default:
		why = firstLineOf(sr.Error)
	}
	rec.Reason = clip("step " + strconv.Itoa(idx+1) + " " + quote(sr.Name) + ": " + why)
}

func firstRule(rs []RuleResult, pick func(RuleResult) bool) string {
	for _, r := range rs {
		if pick(r) {
			return r.String()
		}
	}
	return "(no rule)"
}

func quote(s string) string { return "'" + s + "'" }

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func clip(s string) string {
	s = Redact(s)
	if r := []rune(s); len(r) > reasonMax {
		return string(r[:reasonMax]) + "…"
	}
	return s
}

// runsHint is the pointer every failure notice ends with.
func runsHint(id string) string {
	return "see details: wick_schedule_message action=runs id=" + id + " result=error · action=run id=" + id + " run_id=<id> (output of each step)"
}
