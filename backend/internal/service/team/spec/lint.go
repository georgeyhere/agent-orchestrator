package spec

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Severity says whether a finding blocks release.
type Severity string

const (
	// SeverityError blocks release.
	SeverityError Severity = "error"
	// SeverityWarning is shown to the planner and the Principal but does not block.
	SeverityWarning Severity = "warning"
)

// Finding is one lint result. Field names the offending field, such as
// "tasks[token-parser].steps[2]".
type Finding struct {
	Severity Severity `json:"severity"`
	Field    string   `json:"field"`
	Message  string   `json:"message"`
}

func (f Finding) String() string {
	return fmt.Sprintf("%s %s: %s", f.Severity, f.Field, f.Message)
}

// LintOptions supplies what the lint cannot know from the brief alone.
type LintOptions struct {
	// Files is the repository tree at the base commit. When nil, the checks
	// that read_first and owns paths exist are skipped.
	Files fs.FS
}

// Blocking reports whether any finding blocks release.
func Blocking(findings []Finding) bool {
	return slices.ContainsFunc(findings, func(f Finding) bool { return f.Severity == SeverityError })
}

// Step count the format recommends.
const (
	minSteps = 5
	maxSteps = 15
)

// unresolved matches words that mean the planner left a decision to the worker.
var unresolved = regexp.MustCompile(`(?i)\b(tbd|tbc|todo|fixme|figure out|as appropriate|as needed|somehow|and so on|etc)\b|\?\?\?`)

// Lint runs the deterministic release gate on a brief. Findings are returned
// in field order: brief fields first, then each task in brief order, then
// cross-task findings.
func Lint(b *Brief, opts LintOptions) []Finding {
	l := &linter{opts: opts}
	l.brief(b)
	ids := l.taskIDs(b)
	for i, task := range b.Tasks {
		l.task(b, i, task, ids)
	}
	order, ok := l.graph(b, ids)
	if ok {
		l.ownership(b, order)
	}
	l.sharedInterfaces(b)
	return l.findings
}

// LintWorkOrder lints a single order outside a brief: field presence, text
// and paths only. Graph and cross-task checks need the whole brief.
func LintWorkOrder(w WorkOrder, opts LintOptions) []Finding {
	l := &linter{opts: opts}
	l.task(nil, -1, w, nil)
	return l.findings
}

type linter struct {
	opts     LintOptions
	findings []Finding
}

func (l *linter) add(sev Severity, field, format string, args ...any) {
	l.findings = append(l.findings, Finding{Severity: sev, Field: field, Message: fmt.Sprintf(format, args...)})
}

func (l *linter) errorf(field, format string, args ...any) {
	l.add(SeverityError, field, format, args...)
}

func (l *linter) warnf(field, format string, args ...any) {
	l.add(SeverityWarning, field, format, args...)
}

func (l *linter) required(field, value string) bool {
	if strings.TrimSpace(value) == "" {
		l.errorf(field, "is required")
		return false
	}
	return true
}

func (l *linter) nonEmpty(field string, n int) bool {
	if n == 0 {
		l.errorf(field, "needs at least one entry")
		return false
	}
	return true
}

func (l *linter) text(field, value string) {
	if m := unresolved.FindString(value); m != "" {
		l.errorf(field, "contains unresolved wording %q; make the decision or add an escalate_if trigger", m)
	}
}

func (l *linter) brief(b *Brief) {
	if l.required("goal", b.Goal) {
		l.text("goal", b.Goal)
	}
	if len(b.NonGoals) == 0 {
		l.errorf("non_goals", "needs at least one entry; name what is out of scope")
	}
	for i, ref := range b.Context {
		f := fmt.Sprintf("context[%d]", i)
		l.required(f+".ref", ref.Ref)
		l.required(f+".reason", ref.Reason)
	}
	if len(b.Decisions) == 0 {
		l.warnf("decisions", "is empty; record the choices a worker could get wrong")
	}
	seen := map[string]bool{}
	for i, d := range b.Decisions {
		f := fmt.Sprintf("decisions[%d]", i)
		if l.required(f+".id", d.ID) {
			if seen[d.ID] {
				l.errorf(f+".id", "duplicates decision %q", d.ID)
			}
			seen[d.ID] = true
		}
		if l.required(f+".choice", d.Choice) {
			l.text(f+".choice", d.Choice)
		}
		l.required(f+".rationale", d.Rationale)
	}
	names := map[string]bool{}
	for i, iface := range b.Interfaces {
		f := fmt.Sprintf("interfaces[%d]", i)
		if l.required(f+".name", iface.Name) {
			if names[iface.Name] {
				l.errorf(f+".name", "duplicates interface %q", iface.Name)
			}
			names[iface.Name] = true
		}
		l.required(f+".definition", iface.Definition)
	}
	for i, r := range b.Risks {
		f := fmt.Sprintf("risks[%d]", i)
		l.required(f+".risk", r.Risk)
		l.required(f+".signal", r.Signal)
	}
	if l.nonEmpty("acceptance", len(b.Acceptance)) {
		for i, c := range b.Acceptance {
			l.check(fmt.Sprintf("acceptance[%d]", i), c)
		}
	}
	l.nonEmpty("tasks", len(b.Tasks))
	l.required("staffing.supervisor", b.Staffing.Supervisor)
	for _, role := range sortedKeys(b.Staffing.Tiers) {
		l.tier("staffing.tiers."+role, b.Staffing.Tiers[role])
	}
}

func (l *linter) taskIDs(b *Brief) map[string]int {
	ids := map[string]int{}
	for i, task := range b.Tasks {
		if task.ID == "" {
			continue
		}
		if _, dup := ids[task.ID]; dup {
			l.errorf(fmt.Sprintf("tasks[%d].id", i), "duplicates task %q", task.ID)
			continue
		}
		ids[task.ID] = i
	}
	return ids
}

func taskField(i int, w WorkOrder) string {
	if w.ID != "" {
		return fmt.Sprintf("tasks[%s]", w.ID)
	}
	if i < 0 {
		return "task"
	}
	return fmt.Sprintf("tasks[%d]", i)
}

func (l *linter) task(b *Brief, i int, w WorkOrder, ids map[string]int) {
	f := taskField(i, w)
	if w.legacy {
		l.warnf(f, "is a free-text task from before work orders; it has no owns, checks or escalation triggers")
		return
	}
	if b != nil {
		l.required(f+".id", w.ID)
	}
	if l.required(f+".title", w.Title) {
		l.text(f+".title", w.Title)
	}
	if l.required(f+".outcome", w.Outcome) {
		l.text(f+".outcome", w.Outcome)
	}
	if l.nonEmpty(f+".read_first", len(w.ReadFirst)) {
		for j, ref := range w.ReadFirst {
			rf := fmt.Sprintf("%s.read_first[%d]", f, j)
			if l.required(rf+".path", ref.Path) && l.cleanPath(rf+".path", ref.Path) {
				l.exists(rf+".path", ref.Path)
			}
			l.required(rf+".why", ref.Why)
		}
	}
	if l.nonEmpty(f+".owns", len(w.Owns)) {
		for j, glob := range w.Owns {
			of := fmt.Sprintf("%s.owns[%d]", f, j)
			if l.required(of, glob) && l.cleanPath(of, glob) && l.validGlob(of, glob) {
				l.ownsExists(of, glob)
			}
		}
	}
	if l.nonEmpty(f+".steps", len(w.Steps)) {
		if n := len(w.Steps); n < minSteps || n > maxSteps {
			l.warnf(f+".steps", "has %d steps; aim for %d to %d concrete steps", n, minSteps, maxSteps)
		}
		for j, step := range w.Steps {
			sf := fmt.Sprintf("%s.steps[%d]", f, j+1)
			if l.required(sf, step) {
				l.text(sf, step)
			}
		}
	}
	for j, iface := range w.Interfaces {
		jf := fmt.Sprintf("%s.interfaces[%d]", f, j)
		l.required(jf+".name", iface.Name)
		l.required(jf+".definition", iface.Definition)
	}
	for j, ex := range w.Examples {
		ef := fmt.Sprintf("%s.examples[%d]", f, j)
		l.required(ef+".given", ex.Given)
		l.required(ef+".expect", ex.Expect)
	}
	l.nonEmpty(f+".do_not", len(w.DoNot))
	if l.nonEmpty(f+".checks", len(w.Checks)) {
		runs := 0
		for j, c := range w.Checks {
			l.check(fmt.Sprintf("%s.checks[%d]", f, j), c)
			if c.Kind == CheckRun {
				runs++
			}
		}
		if runs == 0 {
			l.errorf(f+".checks", "needs at least one run check with an expected result")
		}
	}
	if l.nonEmpty(f+".evidence", len(w.Evidence)) {
		for j, e := range w.Evidence {
			switch e {
			case EvidenceRun, EvidenceCommit, EvidencePR, EvidenceNote:
			default:
				l.errorf(fmt.Sprintf("%s.evidence[%d]", f, j), "unknown evidence kind %q; use run, commit, pr or note", e)
			}
		}
	}
	if l.nonEmpty(f+".escalate_if", len(w.EscalateIf)) {
		for j, cond := range w.EscalateIf {
			cf := fmt.Sprintf("%s.escalate_if[%d]", f, j)
			if l.required(cf, cond) {
				l.text(cf, cond)
			}
		}
	}
	if w.Budget.Turns < 0 || w.Budget.WallMinutes < 0 {
		l.errorf(f+".budget", "limits must be positive")
	} else if w.Budget.Turns == 0 && w.Budget.WallMinutes == 0 {
		l.errorf(f+".budget", "needs turns or wall_minutes")
	}
	if w.ModelTier != "" {
		l.tier(f+".model_tier", w.ModelTier)
	}
	if b == nil {
		return
	}
	known := map[string]bool{}
	for _, d := range b.Decisions {
		known[d.ID] = true
	}
	for j, id := range w.Decisions {
		if !known[id] {
			l.errorf(fmt.Sprintf("%s.decisions[%d]", f, j), "unknown decision %q", id)
		}
	}
	for j, dep := range w.DependsOn {
		df := fmt.Sprintf("%s.depends_on[%d]", f, j)
		if dep == w.ID {
			l.errorf(df, "task depends on itself")
		} else if _, ok := ids[dep]; !ok {
			l.errorf(df, "unknown task %q", dep)
		}
	}
}

func (l *linter) tier(field string, t Tier) {
	if t != TierCheap && t != TierStrong {
		l.errorf(field, "unknown model tier %q; use cheap or strong", t)
	}
}

func (l *linter) check(field string, c Check) {
	switch c.Kind {
	case CheckRun:
		if l.required(field+".command", c.Command) {
			if err := parseCommand(c.Command); err != nil {
				l.errorf(field+".command", "does not parse: %v", err)
			}
		}
		if c.Expect == nil || (c.Expect.Exit == nil && c.Expect.OutputContains == "") {
			l.errorf(field+".expect", "is required for run checks, for example {exit: 0}")
		}
	case CheckPRCI:
		if c.Require != RequireGreen && c.Require != RequireNoFailures {
			l.errorf(field+".require", "must be %s or %s", RequireGreen, RequireNoFailures)
		}
	case CheckReviewer:
		l.required(field+".role", c.Role)
	case "":
		l.errorf(field+".kind", "is required: run, pr_ci or reviewer")
	default:
		l.errorf(field+".kind", "unknown check kind %q; use run, pr_ci or reviewer", c.Kind)
	}
}

// cleanPath rejects paths that escape the repository.
func (l *linter) cleanPath(field, p string) bool {
	if path.IsAbs(p) || strings.HasPrefix(p, `\`) || (len(p) > 1 && p[1] == ':') {
		l.errorf(field, "must be relative to the repository root")
		return false
	}
	for _, seg := range strings.Split(strings.TrimSuffix(p, "/"), "/") {
		if seg == ".." {
			l.errorf(field, "must not contain ..")
			return false
		}
	}
	return true
}

func (l *linter) validGlob(field, glob string) bool {
	for _, seg := range strings.Split(strings.TrimSuffix(glob, "/"), "/") {
		if seg == "**" {
			continue
		}
		if strings.Contains(seg, "**") {
			l.errorf(field, "** must be a whole path segment")
			return false
		}
		if _, err := path.Match(seg, ""); err != nil {
			l.errorf(field, "is not a valid glob: %v", err)
			return false
		}
	}
	return true
}

func (l *linter) exists(field, p string) {
	if l.opts.Files == nil {
		return
	}
	if _, err := fs.Stat(l.opts.Files, path.Clean(strings.TrimSuffix(p, "/"))); err != nil {
		l.errorf(field, "%s does not exist at the base commit", p)
	}
}

// ownsExists accepts a new file in an existing directory, so a task may own a
// file it creates. For a glob, the directory before the first wildcard must
// exist.
func (l *linter) ownsExists(field, glob string) {
	if l.opts.Files == nil {
		return
	}
	dir := literalDir(glob)
	if dir == "." {
		return
	}
	info, err := fs.Stat(l.opts.Files, dir)
	if err != nil || !info.IsDir() {
		l.errorf(field, "directory %s does not exist at the base commit", dir)
	}
}

// literalDir is the deepest directory of glob that has no wildcard. For a
// literal file path it is the file's parent; for a trailing-slash directory it
// is the directory itself.
func literalDir(glob string) string {
	if strings.HasSuffix(glob, "/") {
		glob = strings.TrimSuffix(glob, "/") + "/x"
	}
	segs := strings.Split(glob, "/")
	end := len(segs) - 1
	for i, seg := range segs {
		if hasMeta(seg) {
			end = i
			break
		}
	}
	if end == 0 {
		return "."
	}
	return strings.Join(segs[:end], "/")
}

// graph checks depends_on for cycles and returns each task's transitive
// prerequisites. ok is false when a cycle makes concurrency undefined.
func (l *linter) graph(b *Brief, ids map[string]int) (map[string]map[string]bool, bool) {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	ancestors := map[string]map[string]bool{}
	ok := true
	var visit func(id string, stack []string)
	visit = func(id string, stack []string) {
		switch state[id] {
		case done:
			return
		case visiting:
			start := slices.Index(stack, id)
			cycle := append(slices.Clone(stack[start:]), id)
			l.errorf(fmt.Sprintf("tasks[%s].depends_on", id), "cycle: %s", strings.Join(cycle, " -> "))
			ok = false
			return
		}
		state[id] = visiting
		set := map[string]bool{}
		for _, dep := range b.Tasks[ids[id]].DependsOn {
			if _, known := ids[dep]; !known || dep == id {
				continue
			}
			visit(dep, append(stack, id))
			set[dep] = true
			for a := range ancestors[dep] {
				set[a] = true
			}
		}
		ancestors[id] = set
		state[id] = done
	}
	for _, task := range b.Tasks {
		if _, known := ids[task.ID]; known && state[task.ID] == unvisited {
			visit(task.ID, nil)
		}
	}
	return ancestors, ok
}

// ownership reports owns globs that overlap between tasks that can run at the
// same time, meaning neither depends on the other, directly or transitively.
func (l *linter) ownership(b *Brief, ancestors map[string]map[string]bool) {
	for i := 0; i < len(b.Tasks); i++ {
		for j := i + 1; j < len(b.Tasks); j++ {
			a, c := b.Tasks[i], b.Tasks[j]
			if a.ID == "" || c.ID == "" || a.legacy || c.legacy {
				continue
			}
			if ancestors[a.ID][c.ID] || ancestors[c.ID][a.ID] {
				continue
			}
			for _, ga := range a.Owns {
				for _, gc := range c.Owns {
					if globsOverlap(ga, gc) {
						l.errorf(fmt.Sprintf("tasks[%s].owns", c.ID),
							"%s overlaps %s owned by %s, which can run at the same time; add a depends_on edge or split the files",
							gc, ga, a.ID)
					}
				}
			}
		}
	}
}

// sharedInterfaces requires every interface a task copies to match the brief,
// and interfaces shared by two tasks to be identical.
func (l *linter) sharedInterfaces(b *Brief) {
	brief := map[string]string{}
	for _, iface := range b.Interfaces {
		brief[iface.Name] = iface.Definition
	}
	first := map[string]string{}
	owner := map[string]string{}
	for i, task := range b.Tasks {
		f := taskField(i, task)
		for j, iface := range task.Interfaces {
			if iface.Name == "" {
				continue
			}
			jf := fmt.Sprintf("%s.interfaces[%d]", f, j)
			if def, ok := brief[iface.Name]; ok {
				if def != iface.Definition {
					l.errorf(jf, "%s differs from the brief's definition; copy it verbatim", iface.Name)
				}
			} else {
				l.warnf(jf, "%s is not defined in the brief's interfaces", iface.Name)
			}
			if def, ok := first[iface.Name]; ok {
				if def != iface.Definition {
					l.errorf(jf, "%s differs from the definition in %s", iface.Name, owner[iface.Name])
				}
				continue
			}
			first[iface.Name], owner[iface.Name] = iface.Definition, task.ID
		}
	}
}

// parseCommand checks that a check command splits into shell words: quotes
// balance and the command does not end in a dangling escape.
func parseCommand(cmd string) error {
	var quote rune
	escaped := false
	words := 0
	inWord := false
	for _, r := range cmd {
		switch {
		case escaped:
			escaped = false
			inWord = true
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words++
			}
			inWord = false
		default:
			inWord = true
		}
	}
	switch {
	case escaped:
		return fmt.Errorf("ends with an escape")
	case quote != 0:
		return fmt.Errorf("unterminated %c quote", quote)
	}
	if inWord {
		words++
	}
	if words == 0 {
		return fmt.Errorf("has no words")
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
