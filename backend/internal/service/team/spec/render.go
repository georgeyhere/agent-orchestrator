package spec

import (
	"fmt"
	"strings"
)

// RenderWorkOrder renders the prompt a worker receives for one task: the order
// itself plus the mission's goal, non-goals and the decisions the order names
// (all decisions when it names none). Section order is fixed and the output
// holds no revision numbers, so the runtime can use it verbatim as a wake
// prompt and two renders of the same order are identical.
func RenderWorkOrder(b *Brief, taskID string) (string, error) {
	w, ok := b.Task(taskID)
	if !ok {
		return "", fmt.Errorf("unknown task %q", taskID)
	}
	var r renderer
	r.heading(1, "Work order: "+w.Title)
	r.para("**Outcome:** " + w.Outcome)
	if w.legacy {
		r.para("This task was written before work orders. It names no owned files, checks or escalation triggers: ask your supervisor before changing anything you are unsure about.")
	}

	r.heading(2, "Mission goal")
	r.para(b.Goal)
	r.bullets("Out of scope", b.NonGoals)

	decisions := b.Decisions
	if len(w.Decisions) > 0 {
		decisions = nil
		for _, id := range w.Decisions {
			for _, d := range b.Decisions {
				if d.ID == id {
					decisions = append(decisions, d)
				}
			}
		}
	}
	if len(decisions) > 0 {
		r.heading(2, "Decisions already made")
		r.para("Do not reopen these. If one blocks you, escalate.")
		for _, d := range decisions {
			line := fmt.Sprintf("- **%s:** %s. %s", d.ID, strings.TrimSuffix(d.Choice, "."), d.Rationale)
			if len(d.Rejected) > 0 {
				line += " Rejected: " + strings.Join(d.Rejected, "; ") + "."
			}
			r.line(line)
		}
		r.blank()
	}

	var prereqs []string
	for _, dep := range w.DependsOn {
		if t, ok := b.Task(dep); ok {
			prereqs = append(prereqs, fmt.Sprintf("`%s`: %s", t.ID, t.Outcome))
		}
	}
	r.bullets("Already done before this task", prereqs)
	renderOrderBody(&r, w)
	return r.String(), nil
}

func renderOrderBody(r *renderer, w WorkOrder) {
	if len(w.ReadFirst) > 0 {
		r.heading(2, "Read first")
		for _, ref := range w.ReadFirst {
			loc := "`" + ref.Path
			if ref.Symbol != "" {
				loc += ":" + ref.Symbol
			}
			if ref.Lines != "" {
				loc += " lines " + ref.Lines
			}
			r.line("- " + loc + "`: " + ref.Why)
		}
		r.blank()
	}
	r.codeBullets("You may change only", w.Owns)
	if len(w.Steps) > 0 {
		r.heading(2, "Steps")
		for i, step := range w.Steps {
			r.line(fmt.Sprintf("%d. %s", i+1, step))
		}
		r.blank()
	}
	if len(w.Interfaces) > 0 {
		r.heading(2, "Interfaces")
		r.para("Produce or consume these exactly. Do not change them.")
		for _, iface := range w.Interfaces {
			r.line("**" + iface.Name + "**")
			r.blank()
			r.code(iface.Definition)
		}
	}
	if len(w.Examples) > 0 {
		r.heading(2, "Examples")
		for _, ex := range w.Examples {
			r.line("- Given " + ex.Given + ", expect " + ex.Expect)
		}
		r.blank()
	}
	r.bullets("Do not", w.DoNot)
	if len(w.Checks) > 0 {
		r.heading(2, "Checks")
		r.para("Run each command through `ao team run` so its result is recorded.")
		for _, c := range w.Checks {
			r.line("- " + describeCheck(c))
		}
		r.blank()
	}
	if len(w.Evidence) > 0 {
		kinds := make([]string, len(w.Evidence))
		for i, e := range w.Evidence {
			kinds[i] = evidenceLabel(e)
		}
		r.bullets("Evidence to submit", kinds)
	}
	r.bullets("Stop and escalate if", w.EscalateIf)
	if b := describeBudget(w.Budget); b != "" {
		r.heading(2, "Budget")
		r.para(b + ". When it runs out, stop, block the task and report where you are.")
	}
}

// RenderBrief renders a whole brief for the planner, reviewers and the GUI.
func RenderBrief(b *Brief) string {
	var r renderer
	r.heading(1, "Mission brief")
	r.para("**Goal:** " + b.Goal)
	r.bullets("Out of scope", b.NonGoals)
	if len(b.Context) > 0 {
		r.heading(2, "Context")
		for _, c := range b.Context {
			r.line("- `" + c.Ref + "`: " + c.Reason)
		}
		r.blank()
	}
	if len(b.Decisions) > 0 {
		r.heading(2, "Decisions")
		for _, d := range b.Decisions {
			line := fmt.Sprintf("- **%s:** %s. %s", d.ID, strings.TrimSuffix(d.Choice, "."), d.Rationale)
			if len(d.Rejected) > 0 {
				line += " Rejected: " + strings.Join(d.Rejected, "; ") + "."
			}
			r.line(line)
		}
		r.blank()
	}
	if len(b.Interfaces) > 0 {
		r.heading(2, "Interfaces")
		for _, iface := range b.Interfaces {
			r.line("**" + iface.Name + "**")
			r.blank()
			r.code(iface.Definition)
		}
	}
	r.bullets("Constraints", b.Constraints)
	if len(b.Risks) > 0 {
		r.heading(2, "Risks")
		for _, risk := range b.Risks {
			line := "- " + risk.Risk + ". Signal: " + risk.Signal
			if risk.Response != "" {
				line += ". Response: " + risk.Response
			}
			r.line(line)
		}
		r.blank()
	}
	if len(b.Acceptance) > 0 {
		r.heading(2, "Acceptance")
		for _, c := range b.Acceptance {
			r.line("- " + describeCheck(c))
		}
		r.blank()
	}
	if len(b.Tasks) > 0 {
		r.heading(2, "Tasks")
		for _, t := range b.Tasks {
			line := fmt.Sprintf("- `%s` (%s): %s", t.ID, t.Tier(), t.Title)
			if len(t.DependsOn) > 0 {
				line += ". After " + strings.Join(codeAll(t.DependsOn), ", ")
			}
			r.line(line)
		}
		r.blank()
	}
	r.heading(2, "Staffing")
	r.line("- Supervisor: " + orNone(b.Staffing.Supervisor))
	r.line("- Workers: " + orNone(strings.Join(b.Staffing.Workers, ", ")))
	for _, role := range sortedKeys(b.Staffing.Tiers) {
		r.line(fmt.Sprintf("- %s runs on the %s tier", role, b.Staffing.Tiers[role]))
	}
	r.blank()
	return r.String()
}

func describeCheck(c Check) string {
	name := ""
	if c.Name != "" {
		name = c.Name + ": "
	}
	switch c.Kind {
	case CheckRun:
		return name + "`" + c.Command + "` must " + describeExpect(c.Expect)
	case CheckPRCI:
		if c.Require == RequireNoFailures {
			return name + "PR CI has no failing checks"
		}
		return name + "PR CI is green"
	case CheckReviewer:
		return name + "approved by a " + c.Role + " reviewer"
	}
	return name + string(c.Kind)
}

func describeExpect(e *Expect) string {
	if e == nil {
		return "succeed"
	}
	var parts []string
	if e.Exit != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *e.Exit))
	}
	if e.OutputContains != "" {
		parts = append(parts, fmt.Sprintf("print %q", e.OutputContains))
	}
	return strings.Join(parts, " and ")
}

func describeBudget(b Budget) string {
	var parts []string
	if b.Turns > 0 {
		parts = append(parts, fmt.Sprintf("%d turns", b.Turns))
	}
	if b.WallMinutes > 0 {
		parts = append(parts, fmt.Sprintf("%d minutes", b.WallMinutes))
	}
	return strings.Join(parts, " or ")
}

func evidenceLabel(e EvidenceKind) string {
	switch e {
	case EvidenceRun:
		return "The `ao team run` evidence ID of each check"
	case EvidenceCommit:
		return "The commit SHA"
	case EvidencePR:
		return "The PR"
	case EvidenceNote:
		return "A short note on anything the reviewer should know"
	}
	return string(e)
}

func codeAll(items []string) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = "`" + s + "`"
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none yet"
	}
	return s
}

type renderer struct{ b strings.Builder }

func (r *renderer) heading(level int, text string) {
	r.b.WriteString(strings.Repeat("#", level) + " " + text + "\n\n")
}

func (r *renderer) para(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	r.b.WriteString(text + "\n\n")
}

func (r *renderer) line(text string) { r.b.WriteString(text + "\n") }

func (r *renderer) blank() { r.b.WriteString("\n") }

func (r *renderer) bullets(title string, items []string) {
	if len(items) == 0 {
		return
	}
	r.heading(2, title)
	for _, item := range items {
		r.line("- " + item)
	}
	r.blank()
}

func (r *renderer) codeBullets(title string, items []string) {
	r.bullets(title, codeAll(items))
}

func (r *renderer) code(text string) {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	r.b.WriteString(fence + "\n" + strings.TrimRight(text, "\n") + "\n" + fence + "\n\n")
}

func (r *renderer) String() string {
	return strings.TrimRight(r.b.String(), "\n") + "\n"
}
