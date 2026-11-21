package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// claudeChatTeamAgents resolves the real Claude adapter so chat spawn tests
// exercise the production hook installer; every other harness keeps the fake.
type claudeChatTeamAgents struct{ fakeAgents }

func (claudeChatTeamAgents) Agent(h domain.AgentHarness) (ports.Agent, bool) {
	if h == domain.HarnessClaudeCode {
		return claudecode.New(), true
	}
	return fakeAgent{}, true
}

func newClaudeChatTeamManager(launcher ChatLauncher, workspacePath string) *Manager {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	return New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    claudeChatTeamAgents{},
		Workspace: &fakeWorkspace{path: workspacePath},
		Store:     st,
		Messenger: &fakeMessenger{},
		Chat:      launcher,
		Lifecycle: &fakeLCM{store: st},
		DataDir:   "/ao-test-data",
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
}

func readChatTeamHooks(t *testing.T, workspace string) map[string][]hooksjson.MatcherGroup {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workspace, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]hooksjson.MatcherGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config.Hooks
}

func countChatTeamHookCommand(groups []hooksjson.MatcherGroup, command string) int {
	total := 0
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				total++
			}
		}
	}
	return total
}

// A managed Claude Chat session must install the team mailbox hooks into its
// worktree settings before the chat controller starts, through the same
// workspace preparation terminal launches use.
func TestChatSpawnInstallsClaudeTeamHooksBeforeControllerStart(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	launcher := &recordingLauncher{}
	m := newClaudeChatTeamManager(launcher, workspace)
	m.SetTeamInstructions(func(context.Context, domain.ProjectID, domain.SessionID) (string, error) {
		return "team role", nil
	})
	var sawManagedEnv, sawTeamHooksAtStart bool
	launcher.beforeStart = func(cfg ChatStart) {
		sawManagedEnv = cfg.Env["AO_TEAM_MANAGED"] == "1"
		hooks := readChatTeamHooks(t, cfg.WorkspacePath)
		sawTeamHooksAtStart = true
		for _, event := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"} {
			if got := countChatTeamHookCommand(hooks[event], "ao team hook "+event); got != 1 {
				t.Errorf("at controller start: %s team command count = %d, want 1", event, got)
				sawTeamHooksAtStart = false
			}
		}
	}
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !sawManagedEnv {
		t.Fatal("chat controller env lacked AO_TEAM_MANAGED=1")
	}
	if !sawTeamHooksAtStart {
		t.Fatal("team hooks were not installed before the chat controller started")
	}
	if len(launcher.started) != 1 || launcher.started[0].SessionID != rec.ID {
		t.Fatalf("chat controller started %d times, want 1 for %s", len(launcher.started), rec.ID)
	}
}

// An unmanaged Claude Chat session installs activity hooks only: standalone
// behavior is unchanged.
func TestChatSpawnInstallsNoTeamHooksWhenUnmanaged(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	launcher := &recordingLauncher{}
	m := newClaudeChatTeamManager(launcher, workspace)
	m.SetTeamInstructions(func(context.Context, domain.ProjectID, domain.SessionID) (string, error) {
		return "", nil
	})
	if _, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		RequestedMode: domain.SessionModeChat,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	hooks := readChatTeamHooks(t, workspace)
	for event, groups := range hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if strings.HasPrefix(hook.Command, "ao team hook ") {
					t.Fatalf("unmanaged chat installed %s team command %q", event, hook.Command)
				}
			}
		}
	}
	if got := countChatTeamHookCommand(hooks["Stop"], "ao hooks claude-code stop"); got != 1 {
		t.Fatalf("activity stop command count = %d, want 1", got)
	}
}
