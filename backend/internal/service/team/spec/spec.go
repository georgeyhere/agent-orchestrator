// Package spec defines the typed mission brief and task work order that a
// strong planner writes and cheap workers execute, the deterministic lint that
// gates a brief's release, and the markdown rendering used for prompts and the
// GUI.
//
// The package is pure: it holds no storage, runs no commands and reads files
// only through the fs.FS a caller passes to Lint. The team runtime owns
// persistence and decides when to lint, render and release.
package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Brief is the planner's mission record: its goal, the decisions it made, the
// contracts between tasks and the task graph. Every decision a worker could get
// wrong is either made in Decisions or named in Risks with an escalation
// trigger.
type Brief struct {
	// Goal is one outcome, in user terms.
	Goal string `json:"goal" yaml:"goal"`
	// NonGoals is what is explicitly out of scope.
	NonGoals []string `json:"non_goals" yaml:"non_goals"`
	// Context lists files, docs and memory IDs to read, each with a reason.
	Context []ContextRef `json:"context,omitempty" yaml:"context,omitempty"`
	// Decisions are choices already made, with rationale and rejected alternatives.
	Decisions []Decision `json:"decisions,omitempty" yaml:"decisions,omitempty"`
	// Interfaces are contracts between tasks: signatures, schemas, endpoints, error shapes.
	Interfaces []Interface `json:"interfaces,omitempty" yaml:"interfaces,omitempty"`
	// Constraints are conventions, banned approaches, performance and compatibility limits.
	Constraints []string `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	// Risks are known traps and how to spot them.
	Risks []Risk `json:"risks,omitempty" yaml:"risks,omitempty"`
	// Acceptance holds the mission-level checks that define done.
	Acceptance []Check `json:"acceptance" yaml:"acceptance"`
	// Tasks are the work orders; their DependsOn edges form the task graph.
	Tasks []WorkOrder `json:"tasks" yaml:"tasks"`
	// Staffing names the supervisor, the workers and a model tier per role.
	Staffing Staffing `json:"staffing" yaml:"staffing"`
}

// ContextRef points at something a reader should load before working.
type ContextRef struct {
	// Ref is a repository path, a doc URL or a memory ID.
	Ref    string `json:"ref" yaml:"ref"`
	Reason string `json:"reason" yaml:"reason"`
}

// Decision is a settled choice that workers must not reopen.
type Decision struct {
	ID        string   `json:"id" yaml:"id"`
	Choice    string   `json:"choice" yaml:"choice"`
	Rationale string   `json:"rationale" yaml:"rationale"`
	Rejected  []string `json:"rejected,omitempty" yaml:"rejected,omitempty"`
}

// Interface is a named contract. Work orders copy the brief's definition
// verbatim, so two tasks that share an interface agree byte for byte.
type Interface struct {
	Name       string `json:"name" yaml:"name"`
	Definition string `json:"definition" yaml:"definition"`
}

// Risk is a known trap, the signal that shows it is happening, and what to do.
type Risk struct {
	Risk     string `json:"risk" yaml:"risk"`
	Signal   string `json:"signal" yaml:"signal"`
	Response string `json:"response,omitempty" yaml:"response,omitempty"`
}

// Staffing assigns people to the mission. The supervisor may be a chief, the
// orchestrator or any reviewer the project allows; the spec does not decide
// which tiers supervise.
type Staffing struct {
	Supervisor string   `json:"supervisor" yaml:"supervisor"`
	Workers    []string `json:"workers,omitempty" yaml:"workers,omitempty"`
	// Tiers maps a role ID to the model tier it runs on.
	Tiers map[string]Tier `json:"tiers,omitempty" yaml:"tiers,omitempty"`
}

// Tier is the model class an agent runs on. Projects map tiers to concrete
// provider models and effort.
type Tier string

// Model tiers.
const (
	TierCheap  Tier = "cheap"
	TierStrong Tier = "strong"
)

// WorkOrder is one worker's complete instructions. It should be executable
// with no judgment beyond reading code.
type WorkOrder struct {
	// ID is unique within the brief and is what DependsOn refers to.
	ID      string `json:"id" yaml:"id"`
	Title   string `json:"title" yaml:"title"`
	Outcome string `json:"outcome" yaml:"outcome"`
	// ReadFirst lists exact paths, with a symbol or line range, and why each matters.
	ReadFirst []ReadRef `json:"read_first" yaml:"read_first"`
	// Owns lists the globs this task may change. A trailing slash means the
	// whole directory.
	Owns []string `json:"owns" yaml:"owns"`
	// Steps are numbered, concrete edits.
	Steps []string `json:"steps" yaml:"steps"`
	// Interfaces are copied from the brief.
	Interfaces []Interface `json:"interfaces,omitempty" yaml:"interfaces,omitempty"`
	Examples   []Example   `json:"examples,omitempty" yaml:"examples,omitempty"`
	DoNot      []string    `json:"do_not" yaml:"do_not"`
	Checks     []Check     `json:"checks" yaml:"checks"`
	// Evidence lists the kinds of evidence the submission must contain.
	Evidence   []EvidenceKind `json:"evidence" yaml:"evidence"`
	EscalateIf []string       `json:"escalate_if" yaml:"escalate_if"`
	Budget     Budget         `json:"budget" yaml:"budget"`
	DependsOn  []string       `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	// ModelTier defaults to cheap.
	ModelTier Tier `json:"model_tier,omitempty" yaml:"model_tier,omitempty"`
	// Decisions names the brief decisions this worker needs. When empty, the
	// rendered order carries all of them.
	Decisions []string `json:"decisions,omitempty" yaml:"decisions,omitempty"`

	legacy bool
}

// ReadRef is a file to read before starting.
type ReadRef struct {
	Path string `json:"path" yaml:"path"`
	// Symbol or Lines narrow the read; both are optional.
	Symbol string `json:"symbol,omitempty" yaml:"symbol,omitempty"`
	Lines  string `json:"lines,omitempty" yaml:"lines,omitempty"`
	Why    string `json:"why" yaml:"why"`
}

// Example is an input/output pair or test case the result must satisfy.
type Example struct {
	Given  string `json:"given" yaml:"given"`
	Expect string `json:"expect" yaml:"expect"`
}

// CheckKind matches the declared check kinds of the runtime's evidence phase.
type CheckKind string

// Check kinds.
const (
	CheckRun      CheckKind = "run"
	CheckPRCI     CheckKind = "pr_ci"
	CheckReviewer CheckKind = "reviewer"
)

// Check is a mechanical definition of done.
type Check struct {
	Kind CheckKind `json:"kind" yaml:"kind"`
	Name string    `json:"name,omitempty" yaml:"name,omitempty"`
	// Command and Expect apply to run checks.
	Command string  `json:"command,omitempty" yaml:"command,omitempty"`
	Expect  *Expect `json:"expect,omitempty" yaml:"expect,omitempty"`
	// Require applies to pr_ci checks: green or no_failures.
	Require string `json:"require,omitempty" yaml:"require,omitempty"`
	// Role applies to reviewer checks.
	Role string `json:"role,omitempty" yaml:"role,omitempty"`
}

// Expect is the typed result a run check must produce.
type Expect struct {
	Exit           *int   `json:"exit,omitempty" yaml:"exit,omitempty"`
	OutputContains string `json:"output_contains,omitempty" yaml:"output_contains,omitempty"`
}

// PR CI requirements.
const (
	RequireGreen      = "green"
	RequireNoFailures = "no_failures"
)

// EvidenceKind matches the runtime's typed evidence items.
type EvidenceKind string

// Evidence kinds.
const (
	EvidenceRun    EvidenceKind = "run"
	EvidenceCommit EvidenceKind = "commit"
	EvidencePR     EvidenceKind = "pr"
	EvidenceNote   EvidenceKind = "note"
)

// Budget bounds the work before the worker must block and report. At least
// one limit is required.
type Budget struct {
	Turns       int `json:"turns,omitempty" yaml:"turns,omitempty"`
	WallMinutes int `json:"wall_minutes,omitempty" yaml:"wall_minutes,omitempty"`
}

// Tier returns the order's model tier, defaulting to cheap.
func (w WorkOrder) Tier() Tier {
	if w.ModelTier == "" {
		return TierCheap
	}
	return w.ModelTier
}

// Legacy reports whether the order was built from a free-text task.
func (w WorkOrder) Legacy() bool { return w.legacy }

// LegacyWorkOrder wraps a free-text task objective from before work orders
// existed. It renders as a minimal order, and Lint reports it with a single
// warning instead of the missing-field errors.
func LegacyWorkOrder(id, objective string) WorkOrder {
	return WorkOrder{ID: id, Title: firstLine(objective), Outcome: objective, legacy: true}
}

// Task returns the work order with the given ID.
func (b *Brief) Task(id string) (WorkOrder, bool) {
	for _, task := range b.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return WorkOrder{}, false
}

// DecodeBrief reads a brief as JSON or YAML. Unknown fields are rejected so a
// misspelled field cannot silently drop the planner's intent.
func DecodeBrief(r io.Reader) (*Brief, error) {
	var brief Brief
	if err := decode(r, &brief); err != nil {
		return nil, fmt.Errorf("decode brief: %w", err)
	}
	return &brief, nil
}

// DecodeWorkOrder reads a single work order as JSON or YAML.
func DecodeWorkOrder(r io.Reader) (*WorkOrder, error) {
	var order WorkOrder
	if err := decode(r, &order); err != nil {
		return nil, fmt.Errorf("decode work order: %w", err)
	}
	return &order, nil
}

func decode(r io.Reader, out any) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return errors.New("empty document")
	}
	if trimmed[0] == '{' {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.DisallowUnknownFields()
		if err := dec.Decode(out); err != nil {
			return err
		}
		if dec.More() {
			return errors.New("trailing data after JSON document")
		}
		return nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(trimmed))
	dec.KnownFields(true)
	return dec.Decode(out)
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
