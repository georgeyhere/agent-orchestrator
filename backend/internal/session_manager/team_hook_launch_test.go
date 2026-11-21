package sessionmanager

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/muse"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// decodeHookCommand renders a Muse hook command comparable: Windows entries
// are base64(utf16le) PowerShell, POSIX entries are plain shell.
func decodeHookCommand(t *testing.T, command string) string {
	t.Helper()
	const prefix = "powershell.exe -NoProfile -NonInteractive -EncodedCommand "
	encoded, ok := strings.CutPrefix(command, prefix)
	if !ok {
		return command
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		t.Fatalf("decode hook command: %v", err)
	}
	if len(data)%2 != 0 {
		t.Fatalf("odd hook payload length %d", len(data))
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return string(utf16.Decode(units))
}

// pinnedMuseAgent runs the real Muse hook installer with a static launch
// command so the test needs no Muse binary on PATH.
type pinnedMuseAgent struct {
	*muse.Plugin
}

func (pinnedMuseAgent) GetLaunchCommand(context.Context, ports.LaunchConfig) ([]string, error) {
	return []string{"muse-launch"}, nil
}

func (pinnedMuseAgent) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	return []string{"muse-launch"}, true, nil
}

type pinnedMuseAgents struct{ fakeAgents }

func (pinnedMuseAgents) Agent(h domain.AgentHarness) (ports.Agent, bool) {
	if h == domain.HarnessMuse {
		return pinnedMuseAgent{muse.New()}, true
	}
	return fakeAgent{}, true
}

// A managed Muse TUI spawn must pin the supervised launch id into its team
// hook commands: the id in the hooks file has to equal the id the runtime
// launches with, since Muse strips hook environment inheritance.
func TestSpawnPinsMuseTeamHooksToRuntimeLaunch(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	runtime := &fakeRuntime{}
	m := New(Deps{
		Runtime:   runtime,
		Agents:    pinnedMuseAgents{},
		Workspace: &fakeWorkspace{path: workspace},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		DataDir:   dataDir,
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	m.SetTeamInstructions(func(context.Context, domain.ProjectID, domain.SessionID) (string, error) {
		return "team role", nil
	})
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessMuse,
		RequestedMode: domain.SessionModeTUI,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	runtimeID := runtime.lastCfg.Env["AO_RUNTIME_LAUNCH_ID"]
	if runtimeID == "" {
		t.Fatal("runtime launched without a launch id")
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "agent-hooks", "muse", string(rec.ID)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Hooks map[string][]struct {
			Hooks []hooksEntry `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"} {
		for _, group := range decoded.Hooks[event] {
			for _, hook := range group.Hooks {
				rendered := decodeHookCommand(t, hook.Command)
				if strings.Contains(rendered, "team hook "+event) {
					seen++
					if !strings.Contains(rendered, runtimeID) {
						t.Fatalf("%s team hook lacks runtime launch id: %q", event, rendered)
					}
				}
			}
		}
	}
	if seen != 4 {
		t.Fatalf("team hook commands = %d, want 4", seen)
	}
}

type hooksEntry struct {
	Command string `json:"command"`
}
