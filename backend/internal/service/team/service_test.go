package team

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
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
