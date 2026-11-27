package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestTeamEnvironmentOnlyMarksRegisteredSessions(t *testing.T) {
	manager := &Manager{}
	manager.SetTeamInstructions(func(_ context.Context, project domain.ProjectID, session domain.SessionID) (string, error) {
		if project == "project" && session == "team-worker" {
			return "registered role", nil
		}
		return "", nil
	})
	for _, session := range []domain.SessionID{"", "ordinary-worker", "team-worker"} {
		env := map[string]string{"AO_TEAM_MANAGED": "1"}
		if err := manager.applyTeamEnv(session, "project", env); err != nil {
			t.Fatal(err)
		}
		if got, want := env["AO_TEAM_MANAGED"] == "1", session == "team-worker"; got != want {
			t.Fatalf("session %q managed=%v, want %v", session, got, want)
		}
	}
	manager.SetTeamInstructions(func(context.Context, domain.ProjectID, domain.SessionID) (string, error) {
		return "", errors.New("memory unavailable")
	})
	if err := manager.applyTeamEnv("team-worker", "project", map[string]string{}); err == nil {
		t.Fatal("ignored an unavailable team protocol source")
	}
}
