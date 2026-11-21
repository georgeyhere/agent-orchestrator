package claudecode

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	claudeSettingsDirName   = ".claude"
	claudeSettingsFileName  = "settings.local.json"
	claudeHookCommandPrefix = "ao hooks claude-code "
	claudeHookTimeout       = 30
	// claudeTeamHookCommandPrefix identifies the team mailbox hooks. Like the
	// activity hooks above, the command resolves `ao` from PATH: Claude passes
	// its process environment to hook children (the existing activity
	// callbacks already depend on AO_SESSION_ID reaching them), so no
	// session-specific routing is embedded. Keeping the command
	// machine-independent also keeps uninstall/detection exact: no stale
	// absolute executable path can survive a daemon upgrade.
	claudeTeamHookCommandPrefix = "ao team hook "
	claudeTeamHookTimeout       = 30
)

// claudeSessionStartMatcher is referenced by pointer so SessionStart serializes
// with Claude's documented source matcher. "startup" alone misses --resume
// relaunches (and /clear, compact, fork), so the native session id is not
// confirmed under the new RuntimeLaunchID until the next user prompt (#4122).
var claudeSessionStartMatcher = "startup|resume|clear|compact|fork"

// claudeManagedHooks is the source of truth for the hooks AO installs:
// SessionStart (startup/resume/clear/compact/fork), UserPromptSubmit, the tool-use
// trio (PreToolUse, PostToolUse, PostToolUseFailure), PermissionRequest,
// Stop, StopFailure, Notification, and SessionEnd. They report normalized
// session metadata and activity-state signals back into AO's store (see
// DeriveActivityState).
// Notification and SessionEnd carry no matcher: each installs once and fires
// for every sub-type, and the handler filters on the payload's
// notification_type / reason field. The tool-use hooks also carry no matcher
// (fire for every tool): their payloads carry tool_name/tool_use_id, which
// lifecycle uses to clear a stale sticky `blocked` only when the specific
// approved tool finishes — the daemon-side precedence rule is what makes these
// signals safe against parallel-subagent traffic (the naive mapping without it
// was reverted in PR #5's review). PermissionRequest fires when a permission
// dialog appears and carries the blocking tool_name; for worker sessions
// `ao hooks` writes nothing to stdout, so installing it never injects a
// permission decision. Headless reviewer sessions (AO_REVIEW_SESSION_ID set)
// are the one exception: nobody can answer the dialog there, so the hook
// replies with an allow/deny decision (see cli.reviewerPermissionDecision, #4810).
var claudeManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &claudeSessionStartMatcher, Command: claudeHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: claudeHookCommandPrefix + "user-prompt-submit"},
	{Event: "PreToolUse", Command: claudeHookCommandPrefix + "pre-tool-use"},
	{Event: "PostToolUse", Command: claudeHookCommandPrefix + "post-tool-use"},
	{Event: "PostToolUseFailure", Command: claudeHookCommandPrefix + "post-tool-use-failure"},
	{Event: "PermissionRequest", Command: claudeHookCommandPrefix + "permission-request"},
	{Event: "Stop", Command: claudeHookCommandPrefix + "stop"},
	// StopFailure fires instead of Stop when an API error (rate limit, usage
	// limit, unknown model, expired login) ends the turn. Without it such a
	// turn left the session active.
	{Event: "StopFailure", Command: claudeHookCommandPrefix + "stop-failure"},
	{Event: "Notification", Command: claudeHookCommandPrefix + "notification"},
	{Event: "SubagentStart", Command: claudeHookCommandPrefix + "subagent-start"},
	{Event: "SubagentStop", Command: claudeHookCommandPrefix + "subagent-stop"},
	{Event: "SessionEnd", Command: claudeHookCommandPrefix + "session-end"},
}

// claudeHooks manages AO's hooks in the workspace-local
// .claude/settings.local.json file.
var claudeHooks = hooksjson.Manager{
	Label:                 "claude-code",
	CommandPrefix:         claudeHookCommandPrefix,
	LegacyCommandPrefixes: []string{"ao hooks continue "},
	Timeout:               claudeHookTimeout,
	Path:                  claudeSettingsPath,
	Managed:               claudeManagedHooks,
}

// claudeTeamManagedHooks is the team mailbox set installed only for managed
// team sessions: SessionStart/UserPromptSubmit refresh role and memory
// instructions, PostToolUse and Stop surface pending mail, and Stop blocks the
// turn while mail or tracked datashard edits remain. The events mirror the
// daemon team-hook endpoint allowlist. SessionStart shares the activity
// matcher so a --resume relaunch refreshes instructions under the new launch
// generation just as it re-confirms the native session id.
var claudeTeamManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &claudeSessionStartMatcher, Command: claudeTeamHookCommandPrefix + "SessionStart"},
	{Event: "UserPromptSubmit", Command: claudeTeamHookCommandPrefix + "UserPromptSubmit"},
	{Event: "PostToolUse", Command: claudeTeamHookCommandPrefix + "PostToolUse"},
	{Event: "Stop", Command: claudeTeamHookCommandPrefix + "Stop"},
}

// claudeTeamHooks manages the mailbox hooks in the same settings file. It is a
// separate Manager because uninstall and detection match on CommandPrefix, and
// the team commands carry a different prefix than the activity hooks; folding
// them into claudeManagedHooks would leave team entries behind on uninstall
// and invisible to detection.
var claudeTeamHooks = hooksjson.Manager{
	Label:         "claude-code team",
	CommandPrefix: claudeTeamHookCommandPrefix,
	Timeout:       claudeTeamHookTimeout,
	Path:          claudeSettingsPath,
	Managed:       claudeTeamManagedHooks,
}

func claudeSettingsPath(workspacePath string) string {
	return filepath.Join(workspacePath, claudeSettingsDirName, claudeSettingsFileName)
}

// GetAgentHooks installs AO's Claude Code hooks, preserving user-defined hooks and unrelated settings.
// Managed team sessions (AO_TEAM_MANAGED=1) additionally receive the team
// mailbox hooks. Install runs on both spawn and restore, so restored sessions
// re-reconcile both sets without duplicating them.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := claudeHooks.Install(ctx, cfg.WorkspacePath); err != nil {
		return err
	}
	if cfg.Env["AO_TEAM_MANAGED"] != "1" {
		return nil
	}
	return claudeTeamHooks.Install(ctx, cfg.WorkspacePath)
}

// UninstallHooks removes AO's Claude Code hooks, leaving user-defined hooks untouched.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := claudeHooks.Uninstall(ctx, workspacePath); err != nil {
		return err
	}
	return claudeTeamHooks.Uninstall(ctx, workspacePath)
}

// AreHooksInstalled reports whether any AO Claude Code hook is present.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	installed, err := claudeHooks.AreInstalled(ctx, workspacePath)
	if err != nil || installed {
		return installed, err
	}
	return claudeTeamHooks.AreInstalled(ctx, workspacePath)
}
