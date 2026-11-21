package muse

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	museManagedHooksEnvVar = "TBH_MANAGED_HOOKS_PATH"
	museHookCommandPrefix  = "ao hooks muse "
	aoRunFileEnvVar        = "AO_RUN_FILE"
)

type museHooksFile struct {
	Hooks map[string][]hooksjson.MatcherGroup `json:"hooks"`
}

func museManagedHooks(cfg ports.WorkspaceHookConfig) map[string][]hooksjson.MatcherGroup {
	// Muse runs background subagents after the main turn has stopped. Their
	// tool hooks share this managed configuration but do not emit a matching
	// Stop, so installing PreToolUse/PostToolUse can incorrectly reactivate an
	// otherwise idle AO session. Track the top-level turn lifecycle instead;
	// structured input is a runtime-native TUI control and is detected from its
	// terminal marker rather than a tool hook.
	return map[string][]hooksjson.MatcherGroup{
		"SessionStart":      {museHookGroup(cfg, "session-start")},
		"UserPromptSubmit":  {museHookGroup(cfg, "user-prompt-submit")},
		"PermissionRequest": {museHookGroup(cfg, "permission-request")},
		"Stop":              {museHookGroup(cfg, "stop")},
	}
}

func museHookGroup(cfg ports.WorkspaceHookConfig, event string) hooksjson.MatcherGroup {
	return hooksjson.MatcherGroup{Hooks: []hooksjson.HookEntry{{
		Type:    "command",
		Command: museHookCommand(cfg, event),
		Timeout: 5,
	}}}
}

// Muse sanitizes AO_* variables from hook subprocesses. Put the non-sensitive
// callback route directly in AO's per-session hook command so the callback can
// identify its AO session and, in dev mode, the exact daemon that launched it.
func museHookCommand(cfg ports.WorkspaceHookConfig, event string) string {
	return museHookCommandLaunch(cfg, event, "")
}

// museHookCommandLaunch additionally pins the provider generation when the
// caller knows it, so team mailbox callbacks survive the stale-launch fence.
// Activity hooks keep the unpinned shape.
func museHookCommandLaunch(cfg ports.WorkspaceHookConfig, event, launchID string) string {
	if runtime.GOOS == "windows" {
		return museWindowsCommandLaunch(cfg, "ao", "hooks muse "+event, launchID)
	}
	assignments := []string{
		"AO_SESSION_ID=" + museShellQuote(strings.TrimSpace(cfg.SessionID)),
		"AO_DATA_DIR=" + museShellQuote(strings.TrimSpace(cfg.DataDir)),
	}
	if runFile := strings.TrimSpace(os.Getenv(aoRunFileEnvVar)); runFile != "" {
		assignments = append(assignments, aoRunFileEnvVar+"="+museShellQuote(runFile))
	}
	if launchID != "" {
		assignments = append(assignments, "AO_RUNTIME_LAUNCH_ID="+museShellQuote(launchID))
	}
	return "env " + strings.Join(assignments, " ") + " " + museHookCommandPrefix + event
}

// Use an explicit PowerShell process on Windows, independent of Muse's hook
// shell. Encoding preserves paths containing spaces, quotes or shell syntax.
func museWindowsCommand(cfg ports.WorkspaceHookConfig, executable, arguments string) string {
	return museWindowsCommandLaunch(cfg, executable, arguments, "")
}

func museWindowsCommandLaunch(cfg ports.WorkspaceHookConfig, executable, arguments, launchID string) string {
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	script := "$env:AO_SESSION_ID=" + quote(cfg.SessionID) + ";$env:AO_DATA_DIR=" + quote(cfg.DataDir) + ";"
	if runFile := strings.TrimSpace(os.Getenv(aoRunFileEnvVar)); runFile != "" {
		script += "$env:AO_RUN_FILE=" + quote(runFile) + ";"
	}
	if launchID != "" {
		script += "$env:AO_RUNTIME_LAUNCH_ID=" + quote(launchID) + ";"
	}
	script += "& " + quote(executable) + " " + arguments + ";exit $LASTEXITCODE"
	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return "powershell.exe -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(data)
}

func museTeamHookCommand(cfg ports.WorkspaceHookConfig, executable, event string) string {
	return museTeamHookCommandLaunch(cfg, executable, event, "")
}

// museTeamHookCommandLaunch pins the provider generation when known. Muse
// strips AO_* from hook subprocesses, so without the pinned id its mailbox
// callbacks cannot pass the stale-launch fence.
func museTeamHookCommandLaunch(cfg ports.WorkspaceHookConfig, executable, event, launchID string) string {
	if runtime.GOOS == "windows" {
		return museWindowsCommandLaunch(cfg, executable, "team hook "+event, launchID)
	}
	command := museHookCommandLaunch(cfg, event, launchID)
	return strings.TrimSuffix(command, museHookCommandPrefix+event) + museShellQuote(executable) + " team hook " + event
}

func museShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// GetAgentHooks writes Muse's managed-hook configuration under AO's data
// directory. Muse receives its path through a process-local environment
// variable, so this does not create or modify any file in the project.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return p.writeHooksFile(ctx, cfg, "", "muse.GetAgentHooks")
}

// PrepareRuntimeLaunch reissues the managed-hook file once the runtime
// generation is assigned, pinning it into the team mailbox commands. Like
// MiniMax, Muse strips AO_* from hook children, so only the pinned commands
// carry the identity the stale-launch fence requires. Unmanaged sessions and
// launches without an assigned id keep the GetAgentHooks output.
func (p *Plugin) PrepareRuntimeLaunch(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if cfg.Env["AO_TEAM_MANAGED"] != "1" {
		return nil
	}
	launchID := strings.TrimSpace(cfg.Env["AO_RUNTIME_LAUNCH_ID"])
	if launchID == "" {
		return nil
	}
	return p.writeHooksFile(ctx, cfg, launchID, "muse.PrepareRuntimeLaunch")
}

func (p *Plugin) writeHooksFile(ctx context.Context, cfg ports.WorkspaceHookConfig, launchID, caller string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := museManagedHooksPath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return fmt.Errorf("%s: %w", caller, err)
	}
	hooks := museManagedHooks(cfg)
	if cfg.Env["AO_TEAM_MANAGED"] == "1" {
		executable, err := agentlaunch.Executable()
		if err != nil {
			return fmt.Errorf("%s: resolve AO executable: %w", caller, err)
		}
		for _, event := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"} {
			hooks[event] = append(hooks[event], hooksjson.MatcherGroup{Hooks: []hooksjson.HookEntry{{
				Type: "command", Command: museTeamHookCommandLaunch(cfg, executable, event, launchID), Timeout: 30,
			}}})
		}
	}
	data, err := json.MarshalIndent(museHooksFile{Hooks: hooks}, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: marshal hooks: %w", caller, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("%s: create hooks directory: %w", caller, err)
	}
	if err := hookutil.AtomicWriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("%s: write hooks: %w", caller, err)
	}
	return nil
}

// CleanupWorkspace removes the AO-owned managed-hook file. The method name is
// part of the shared agent lifecycle; Muse's project workspace stays untouched.
func (p *Plugin) CleanupWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := museManagedHooksPath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return fmt.Errorf("muse.CleanupWorkspace: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("muse.CleanupWorkspace: remove hooks: %w", err)
	}
	return nil
}

func museManagedHooksPath(dataDir, sessionID string) (string, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return "", errors.New("DataDir is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("SessionID is required")
	}
	if sessionID == "." || filepath.Base(sessionID) != sessionID {
		return "", errors.New("SessionID must be a single path component")
	}
	return filepath.Join(dataDir, "agent-hooks", adapterID, sessionID+".json"), nil
}

func museSystemPromptText(inline, file string) (string, error) {
	if strings.TrimSpace(inline) != "" {
		return strings.TrimRight(inline, "\n"), nil
	}
	if strings.TrimSpace(file) == "" {
		return "", nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
	if err != nil {
		return "", fmt.Errorf("read system prompt file: %w", err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}
