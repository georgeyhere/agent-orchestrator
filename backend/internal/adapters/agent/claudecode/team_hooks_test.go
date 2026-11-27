package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func readClaudeTeamSettings(t *testing.T, workspace string) map[string][]hooksjson.MatcherGroup {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workspace, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks       map[string][]hooksjson.MatcherGroup `json:"hooks"`
		Permissions json.RawMessage                     `json:"permissions"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config.Hooks
}

func teamHookTimeout(t *testing.T, groups []hooksjson.MatcherGroup, command string) int {
	t.Helper()
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				return hook.Timeout
			}
		}
	}
	t.Fatalf("command %q not installed", command)
	return 0
}

// Managed team sessions receive the four mailbox boundaries alongside the
// activity hooks. Install runs on both spawn and restore, so a second install
// must reconcile without duplicating either set.
func TestTeamHooksInstallMailboxBoundariesWhenManaged(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	workspace := t.TempDir()
	settingsPath := filepath.Join(workspace, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"my own stop hook","timeout":5}]}]},"permissions":{"defaultMode":"plan"}}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: map[string]string{"AO_TEAM_MANAGED": "1"}}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	hooks := readClaudeTeamSettings(t, workspace)
	for _, spec := range claudeTeamManagedHooks {
		if got := countClaudeHookCommand(hooks[spec.Event], spec.Command); got != 1 {
			t.Fatalf("%s team command %q count = %d, want 1", spec.Event, spec.Command, got)
		}
		if timeout := teamHookTimeout(t, hooks[spec.Event], spec.Command); timeout != 30 {
			t.Fatalf("%s team hook timeout = %d, want 30", spec.Event, timeout)
		}
	}
	if m := matcherForCommand(hooks["SessionStart"], "ao team hook SessionStart"); m == nil || *m != "startup|resume|clear|compact|fork" {
		t.Fatalf("team SessionStart matcher = %v, want startup|resume|clear|compact|fork", m)
	}
	// Activity hooks stay installed beside the mailbox set.
	if got := countClaudeHookCommand(hooks["Stop"], "ao hooks claude-code stop"); got != 1 {
		t.Fatalf("activity stop command count = %d, want 1", got)
	}
	if got := countClaudeHookCommand(hooks["PostToolUse"], "ao hooks claude-code post-tool-use"); got != 1 {
		t.Fatalf("activity post-tool-use command count = %d, want 1", got)
	}
	// User hook and unrelated settings preserved.
	if countClaudeHookCommand(hooks["Stop"], "my own stop hook") != 1 {
		t.Fatalf("existing Stop hook not preserved: %#v", hooks["Stop"])
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"defaultMode": "plan"`) {
		t.Fatalf("unrelated settings clobbered: %s", data)
	}
}

// Ordinary sessions install no mailbox hooks: standalone behavior is unchanged
// with or without the managed marker.
func TestTeamHooksAbsentWhenUnmanaged(t *testing.T) {
	for _, env := range []map[string]string{nil, {}, {"AO_TEAM_MANAGED": "0"}} {
		p := &Plugin{resolvedBinary: "claude"}
		workspace := t.TempDir()
		cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: env}
		if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		hooks := readClaudeTeamSettings(t, workspace)
		for event, groups := range hooks {
			for _, group := range groups {
				for _, hook := range group.Hooks {
					if strings.HasPrefix(hook.Command, claudeTeamHookCommandPrefix) {
						t.Fatalf("unmanaged install added %s team command %q", event, hook.Command)
					}
				}
			}
		}
		if got := countClaudeHookCommand(hooks["Stop"], "ao hooks claude-code stop"); got != 1 {
			t.Fatalf("activity stop command count = %d, want 1", got)
		}
	}
}

// Uninstall must remove the mailbox entries even though they carry a different
// command prefix than the activity hooks, while leaving user hooks alone.
func TestUninstallRemovesTeamHooks(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	workspace := t.TempDir()
	settingsPath := filepath.Join(workspace, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"my own stop hook","timeout":5}]}]}}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: map[string]string{"AO_TEAM_MANAGED": "1"}}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := p.UninstallHooks(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	hooks := readClaudeTeamSettings(t, workspace)
	for event, groups := range hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if strings.HasPrefix(hook.Command, claudeHookCommandPrefix) || strings.HasPrefix(hook.Command, claudeTeamHookCommandPrefix) {
					t.Fatalf("uninstall left %s AO command %q", event, hook.Command)
				}
			}
		}
	}
	if countClaudeHookCommand(hooks["Stop"], "my own stop hook") != 1 {
		t.Fatalf("existing Stop hook not preserved: %#v", hooks["Stop"])
	}
	if installed, err := p.AreHooksInstalled(context.Background(), workspace); err != nil || installed {
		t.Fatalf("AreHooksInstalled after uninstall = (%v, %v), want (false, nil)", installed, err)
	}
}

// Detection must see a file holding only mailbox entries: matching on the
// activity prefix alone would miss them.
func TestAreHooksInstalledDetectsTeamHooksOnly(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	workspace := t.TempDir()
	settingsPath := filepath.Join(workspace, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	teamOnly := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"ao team hook Stop","timeout":30}]}]}}`
	if err := os.WriteFile(settingsPath, []byte(teamOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	if installed, err := p.AreHooksInstalled(context.Background(), workspace); err != nil || !installed {
		t.Fatalf("AreHooksInstalled with team hooks only = (%v, %v), want (true, nil)", installed, err)
	}
	userOnly := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"my own stop hook","timeout":5}]}]}}`
	if err := os.WriteFile(settingsPath, []byte(userOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	if installed, err := p.AreHooksInstalled(context.Background(), workspace); err != nil || installed {
		t.Fatalf("AreHooksInstalled with user hooks only = (%v, %v), want (false, nil)", installed, err)
	}
}

// Team commands resolve `ao` from PATH with no embedded routing: Claude passes
// its process environment (AO_SESSION_ID and friends) to hook children, which
// the existing activity callbacks already rely on. The exact, platform-neutral
// shape is what keeps install reconciliation, uninstall, and detection exact.
func TestTeamHookCommandsResolveAOFromPath(t *testing.T) {
	if len(claudeTeamManagedHooks) != 4 {
		t.Fatalf("team hooks = %d, want the 4 mailbox events", len(claudeTeamManagedHooks))
	}
	for _, spec := range claudeTeamManagedHooks {
		if want := "ao team hook " + spec.Event; spec.Command != want {
			t.Fatalf("%s team command = %q, want %q", spec.Event, spec.Command, want)
		}
		for _, embedded := range []string{"=", "$", "powershell", "AO_SESSION_ID", "AO_DATA_DIR"} {
			if strings.Contains(spec.Command, embedded) {
				t.Fatalf("%s team command %q embeds routing %q", spec.Event, spec.Command, embedded)
			}
		}
	}
}
