package team

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type projectFixture struct{ path string }

func (p projectFixture) Get(context.Context, domain.ProjectID) (projectsvc.GetResult, error) {
	return projectsvc.GetResult{Status: "ok", Project: &projectsvc.Project{Path: p.path}}, nil
}
func (p projectFixture) List(context.Context) ([]projectsvc.Summary, error) { return nil, nil }

func TestTeamPythonStatusUsesEmbeddedRuntime(t *testing.T) {
	python, err := pythonCommand()
	if err != nil {
		t.Skip(err)
	}
	probe := exec.Command(python, "-c", "import sys; assert sys.version_info >= (3,11)")
	if err := probe.Run(); err != nil {
		t.Skip("Python 3.11+ is required for team integration")
	}
	root := t.TempDir()
	service := New(filepath.Join(root, "data"), projectFixture{root}, nil, slog.Default())
	result, err := service.Execute(context.Background(), "project", Request{Operation: "status"})
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Enabled bool  `json:"enabled"`
		Agents  []any `json:"agents"`
	}
	if err := json.Unmarshal(result, &state); err != nil {
		t.Fatal(err)
	}
	if state.Enabled || len(state.Agents) != 0 {
		t.Fatalf("unexpected initial state: %s", result)
	}
	if _, err := os.Stat(filepath.Join(root, "data", "teams", "project", "team.sqlite3")); !os.IsNotExist(err) {
		t.Fatal("read-only status initialized an unconfigured team")
	}
}

func TestTeamRejectsInternalOperationsAndInvalidProjectPaths(t *testing.T) {
	service := New(t.TempDir(), projectFixture{t.TempDir()}, nil, slog.Default())
	for _, operation := range []string{"attach", "resolve", "dispatch", "launch-result"} {
		if _, err := service.Execute(context.Background(), "project", Request{Operation: operation}); err == nil {
			t.Fatalf("public action accepted %q", operation)
		}
	}
	for _, id := range []string{"../outside", "project/other", "..", ""} {
		if _, err := service.statePath(domain.ProjectID(id)); err == nil {
			t.Fatalf("accepted project path %q", id)
		}
	}
}

func TestEmbeddedRuntimeHasNoMachineSpecificPaths(t *testing.T) {
	data, err := runtimeFiles.ReadFile("python/aoteam/runtime.py")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `C:\Users\`) {
		t.Fatal("runtime contains a machine-specific path")
	}
}

type sessionFixture struct {
	project domain.ProjectID
	path    string
}

func (s sessionFixture) GetRecord(_ context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	return domain.SessionRecord{ID: id, ProjectID: s.project, Metadata: domain.SessionMetadata{WorkspacePath: s.path}}, nil
}

func (s sessionFixture) Get(context.Context, domain.SessionID) (domain.Session, error) {
	return domain.Session{}, fmt.Errorf("unused session lookup")
}

func (s sessionFixture) Spawn(context.Context, ports.SpawnConfig) (domain.Session, int, int, error) {
	return domain.Session{}, 0, 0, fmt.Errorf("unused session spawn")
}

func (s sessionFixture) Send(context.Context, domain.SessionID, string, *ports.SpawnAttachment) error {
	return fmt.Errorf("unused session send")
}

func (s sessionFixture) ExitAgent(context.Context, domain.SessionID) (sessionsvc.ExitAgentOutcome, error) {
	return sessionsvc.ExitAgentOutcome{}, fmt.Errorf("unused agent exit")
}

func (s sessionFixture) ResumeAgent(context.Context, domain.SessionID) (sessionsvc.ResumeAgentOutcome, error) {
	return sessionsvc.ResumeAgentOutcome{}, fmt.Errorf("unused agent resume")
}

// TestTeamSupervisionModesThroughDaemonAPI drives both supervision modes
// through the same service entrypoint the daemon controller and CLI use,
// with the embedded Python runtime and per-agent sessions.
func TestTeamSupervisionModesThroughDaemonAPI(t *testing.T) {
	python, err := pythonCommand()
	if err != nil {
		t.Skip(err)
	}
	for _, probe := range []string{
		"import sys; assert sys.version_info >= (3,11)",
		"import os, sys; sys.path.insert(0, os.environ['AO_LLMWIKI_SOURCE']); import llmwiki",
	} {
		if err := exec.Command(python, "-c", probe).Run(); err != nil {
			t.Skip("Python 3.11+ with llmwiki is required for team integration")
		}
	}
	root := t.TempDir()
	project := domain.ProjectID("project")
	fixture := projectFixture{filepath.Join(root, "proj")}
	if err := os.MkdirAll(fixture.path, 0o755); err != nil {
		t.Fatal(err)
	}
	service := New(filepath.Join(root, "data"), fixture, sessionFixture{project, fixture.path}, slog.Default())
	ctx := context.Background()
	act := func(session, operation string, arguments map[string]any) map[string]any {
		t.Helper()
		encoded, err := json.Marshal(arguments)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := service.Execute(ctx, project, Request{Operation: operation, SessionID: session, Arguments: encoded})
		if err != nil {
			t.Fatalf("%s %s: %v", operation, session, err)
		}
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	act("", "enable", map[string]any{"memory_root": filepath.Join(root, "memory")})
	for _, spec := range [][3]string{
		{"orch", "orchestrator", ""},
		{"chief", "chief", "orch"},
		{"worker", "worker", "chief"},
		{"direct", "worker", "orch"},
	} {
		agent, role, parent := spec[0], spec[1], spec[2]
		args := map[string]any{"agent": agent, "role": role}
		if parent != "" {
			args["parent"] = parent
		}
		act("", "agent-add", args)
		if _, err := service.call(ctx, project, "", "attach", map[string]any{"agent": agent, "session_id": "session-" + agent}); err != nil {
			t.Fatal(err)
		}
	}
	created := act("session-orch", "mission-create", map[string]any{
		"mission": "direct", "supervisor": "orch", "objective": "Work", "criteria": "Verify", "workers": []string{"direct"},
	})
	if created["supervisor"] != "orch" || created["orchestrator"] != "orch" {
		t.Fatalf("direct mission=%v", created)
	}
	act("session-orch", "task-assign", map[string]any{"task": "direct-task", "mission": "direct", "worker": "direct", "objective": "Work"})
	act("session-direct", "submit", map[string]any{"kind": "task", "record": "direct-task", "revision": 1, "result": "Done", "evidence": "Tests"})
	if _, err := service.Execute(ctx, project, Request{Operation: "review", SessionID: "session-chief",
		Arguments: json.RawMessage(`{"kind":"task","record":"direct-task","revision":2,"approve":true,"feedback":"Chief"}`)}); err == nil {
		t.Fatal("chief reviewed a directly supervised task")
	}
	act("session-orch", "review", map[string]any{"kind": "task", "record": "direct-task", "revision": 2, "approve": true, "feedback": "Verified"})
	act("session-orch", "submit", map[string]any{"kind": "mission", "record": "direct", "revision": 1, "result": "Integrated", "evidence": "Checks"})
	signed := act("session-orch", "review", map[string]any{"kind": "mission", "record": "direct", "revision": 2, "approve": true, "feedback": "Final signoff"})
	if signed["status"] != "approved" {
		t.Fatalf("direct mission status=%v", signed["status"])
	}
	legacy := act("session-orch", "mission-create", map[string]any{
		"mission": "led", "chief": "chief", "objective": "Work", "criteria": "Verify", "workers": []string{"worker"},
	})
	if legacy["supervisor"] != "chief" {
		t.Fatalf("legacy mission=%v", legacy)
	}
	act("session-chief", "task-assign", map[string]any{"task": "led-task", "mission": "led", "worker": "worker", "objective": "Work"})
	act("session-worker", "submit", map[string]any{"kind": "task", "record": "led-task", "revision": 1, "result": "Done", "evidence": "Tests"})
	act("session-chief", "review", map[string]any{"kind": "task", "record": "led-task", "revision": 2, "approve": true, "feedback": "Verified"})
	act("session-chief", "submit", map[string]any{"kind": "mission", "record": "led", "revision": 1, "result": "Integrated", "evidence": "Checks"})
	signed = act("session-orch", "review", map[string]any{"kind": "mission", "record": "led", "revision": 2, "approve": true, "feedback": "Final signoff"})
	if signed["status"] != "approved" {
		t.Fatalf("legacy mission status=%v", signed["status"])
	}
}
