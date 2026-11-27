package sessionmanager

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SetTeamInstructions installs the optional role and memory policy source before
// the manager starts accepting sessions. Native workers retain their runtime kind.
func (m *Manager) SetTeamInstructions(source func(context.Context, domain.ProjectID, domain.SessionID) (string, error)) {
	m.teamInstructionsMu.Lock()
	defer m.teamInstructionsMu.Unlock()
	m.teamInstructions = source
}

func (m *Manager) applyTeamEnv(id domain.SessionID, project domain.ProjectID, env map[string]string) error {
	deleteProtectedEnv(env, "AO_TEAM_MANAGED", envKeysCaseInsensitive)
	m.teamInstructionsMu.RLock()
	source := m.teamInstructions
	m.teamInstructionsMu.RUnlock()
	if source == nil || id == "" || project == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	instructions, err := source(ctx, project, id)
	if err == nil && instructions != "" {
		setProtectedEnv(env, "AO_TEAM_MANAGED", "1", envKeysCaseInsensitive)
	}
	return err
}
