package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	teamsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/team"
	"github.com/go-chi/chi/v5"
)

type fenceProjectFixture struct{ path string }

func (p fenceProjectFixture) Get(context.Context, domain.ProjectID) (projectsvc.GetResult, error) {
	return projectsvc.GetResult{Status: "ok", Project: &projectsvc.Project{Path: p.path}}, nil
}
func (fenceProjectFixture) List(context.Context) ([]projectsvc.Summary, error) { return nil, nil }

type fenceSessionFixture struct {
	project  domain.ProjectID
	path     string
	launchID string
}

func (f fenceSessionFixture) GetRecord(_ context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	return domain.SessionRecord{ID: id, ProjectID: f.project, Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{WorkspacePath: f.path, RuntimeLaunchID: f.launchID}}, nil
}
func (fenceSessionFixture) Get(context.Context, domain.SessionID) (domain.Session, error) {
	return domain.Session{}, nil
}
func (fenceSessionFixture) Spawn(context.Context, ports.SpawnConfig) (domain.Session, int, int, error) {
	return domain.Session{}, 0, 0, nil
}
func (fenceSessionFixture) Send(context.Context, domain.SessionID, string, *ports.SpawnAttachment) error {
	return nil
}
func (fenceSessionFixture) ExitAgent(context.Context, domain.SessionID) (sessionsvc.ExitAgentOutcome, error) {
	return sessionsvc.ExitAgentOutcome{}, nil
}
func (fenceSessionFixture) ResumeAgent(context.Context, domain.SessionID) (sessionsvc.ResumeAgentOutcome, error) {
	return sessionsvc.ResumeAgentOutcome{}, nil
}

func fencePython(t *testing.T) {
	t.Helper()
	python := strings.TrimSpace(os.Getenv("AO_TEAM_PYTHON"))
	if python == "" {
		if found, err := exec.LookPath("python3"); err == nil {
			python = found
		} else {
			t.Skip("AO_TEAM_PYTHON or python3 is required for team integration")
		}
	}
	for _, probe := range []string{
		"import sys; assert sys.version_info >= (3,11)",
		"import os, sys; sys.path.insert(0, os.environ['AO_LLMWIKI_SOURCE']); import llmwiki",
	} {
		if err := exec.Command(python, "-c", probe).Run(); err != nil {
			t.Skip("Python 3.11+ with llmwiki is required for team integration")
		}
	}
}

// TestTeamHookDropsStaleLaunchEndToEnd posts mailbox hooks through HTTP
// against the real team service: a callback naming a replaced launch reads
// nothing and marks nothing (its mail stays unacknowledgable), while the
// current launch delivers and acknowledges. It speaks only HTTP+JSON, so it
// also runs against the pre-fence code — where the stale post delivers and
// the test fails.
func TestTeamHookDropsStaleLaunchEndToEnd(t *testing.T) {
	fencePython(t)
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	project := domain.ProjectID("project")
	projDir := filepath.Join(root, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessions := fenceSessionFixture{project: project, path: projDir, launchID: "launch-live"}
	svc := teamsvc.New(dataDir, fenceProjectFixture{path: projDir}, sessions, slog.Default())
	router := chi.NewRouter()
	(&TeamController{Svc: svc}).Register(router)
	ctx := context.Background()
	act := func(session, operation string, arguments map[string]any) map[string]any {
		t.Helper()
		encoded, err := json.Marshal(arguments)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := svc.Execute(ctx, project, teamsvc.Request{Operation: operation, SessionID: session, Arguments: encoded})
		if err != nil {
			t.Fatalf("%s %s: %v", operation, session, err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	act("", "enable", map[string]any{"memory_root": filepath.Join(root, "memory")})
	act("", "agent-add", map[string]any{"agent": "orch", "role": "orchestrator"})
	mail := act("", "send", map[string]any{"recipient": "orch", "subject": "stale-probe", "body": "probe", "key": "stale-probe"})
	mailID, _ := mail["id"].(string)
	if mailID == "" {
		t.Fatalf("send returned no id: %v", mail)
	}
	hook := func(body string) string {
		t.Helper()
		request := httptest.NewRequest("POST", "/sessions/session-orch/team-hook", strings.NewReader(body))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("hook %s: status %d", body, response.Code)
		}
		return response.Body.String()
	}
	// A replaced launch's callback reads nothing...
	if out := hook(`{"event":"Stop","launchId":"launch-dead"}`); !strings.Contains(out, `"result":{}`) {
		t.Fatalf("stale hook delivered: %s", out)
	}
	// ...marks nothing (the ack still requires a delivery)...
	ack := func() error {
		encoded, _ := json.Marshal(map[string]any{"message": mailID})
		_, err := svc.Execute(ctx, project, teamsvc.Request{Operation: "ack", SessionID: "session-orch", Arguments: encoded})
		return err
	}
	if err := ack(); err == nil {
		t.Fatal("ack succeeded although the stale hook was dropped")
	}
	// ...while the current launch delivers and acknowledges.
	if out := hook(`{"event":"Stop","launchId":"launch-live"}`); !strings.Contains(out, "decision") {
		t.Fatalf("current hook dropped: %s", out)
	}
	if err := ack(); err != nil {
		t.Fatalf("ack after delivery: %v", err)
	}
}
