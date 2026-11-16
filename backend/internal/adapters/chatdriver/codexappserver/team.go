package codexappserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	codexagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
)

// App-server does not apply the TUI's hook-trust bypass flag. Read Codex's own
// fingerprints, vet our exact declarations, and trust only those definitions in
// this thread's session config. No persisted user settings are changed.
func teamThreadConfig(ctx context.Context, connection *conn, workspace string, env map[string]string, config map[string]any) (map[string]any, error) {
	if env["AO_SESSION_ID"] == "" || env["AO_TEAM_MANAGED"] != "1" {
		return config, nil
	}
	executable, err := agentlaunch.Executable()
	if err != nil {
		return nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var response codexproto.HooksListResponse
	if err := connection.request(probeCtx, "hooks/list", map[string]any{"cwds": []string{workspace}}, &response); err != nil {
		return nil, fmt.Errorf("inspect team lifecycle hooks: %w", err)
	}
	states := trustedTeamHooks(response, codexagent.TeamHookCommands(executable))
	if len(states) != 4 {
		return nil, fmt.Errorf("Codex did not expose all four enabled team lifecycle hooks. Check its hooks policy and version")
	}
	if config == nil {
		config = make(map[string]any)
	}
	// Config keys are paths. A nested hooks table would replace the hook
	// declarations from the launch flags instead of retaining them.
	config["hooks.state"] = states
	return config, nil
}

func trustedTeamHooks(response codexproto.HooksListResponse, commands map[string]string) map[string]any {
	states := make(map[string]any)
	for _, entry := range response.Data {
		for _, hook := range entry.Hooks {
			event := string(hook.EventName)
			if event == "" {
				continue
			}
			command, ours := commands[strings.ToUpper(event[:1])+event[1:]]
			if !ours || hook.Source != codexproto.HookSourceSessionFlags || !hook.Enabled ||
				hook.HandlerType != codexproto.HookHandlerTypeCommand || hook.Command == nil || *hook.Command != command ||
				(hook.Matcher != nil && *hook.Matcher != "") || hook.TimeoutSec != 30 || hook.CurrentHash == "" {
				continue
			}
			states[hook.Key] = map[string]any{"trusted_hash": hook.CurrentHash}
		}
	}
	return states
}
