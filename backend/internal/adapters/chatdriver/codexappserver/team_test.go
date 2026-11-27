package codexappserver

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
)

func TestAppServerTeamHooksKeepStartupCommand(t *testing.T) {
	plain := appServerArgv("codex", nil)
	if len(plain) != 2 || plain[1] != "app-server" {
		t.Fatalf("unexpected unmanaged argv: %q", plain)
	}
	ordinary := appServerArgv("codex", []string{"AO_SESSION_ID=project-1"})
	if len(ordinary) != 2 {
		t.Fatalf("changed an ordinary AO session: %q", ordinary)
	}
	managed := appServerArgv("codex", []string{"AO_SESSION_ID=project-1", "AO_TEAM_MANAGED=1"})
	if len(managed) < 4 || managed[1] != "app-server" {
		t.Fatalf("invalid managed argv: %q", managed)
	}
	joined := strings.Join(managed, " ")
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"} {
		if !strings.Contains(joined, "hooks."+event+"=") || !strings.Contains(joined, "team hook "+event) {
			t.Fatalf("missing mailbox hook %s", event)
		}
	}
}

func TestAppServerTeamTrustOnlyExactSessionHooks(t *testing.T) {
	command := "ao team hook Stop"
	other := "do-something-else"
	matcher := "something"
	base := codexproto.HookMetadata{
		Key: "ours", CurrentHash: "sha256:ours", Command: &command,
		EventName: codexproto.HookEventNameStop, Source: codexproto.HookSourceSessionFlags,
		HandlerType: codexproto.HookHandlerTypeCommand, Enabled: true, TimeoutSec: 30,
	}
	for _, test := range []struct {
		name   string
		mutate func(*codexproto.HookMetadata)
	}{
		{"different command", func(h *codexproto.HookMetadata) { h.Command = &other }},
		{"project hook", func(h *codexproto.HookMetadata) { h.Source = codexproto.HookSourceProject }},
		{"disabled", func(h *codexproto.HookMetadata) { h.Enabled = false }},
		{"filtered", func(h *codexproto.HookMetadata) { h.Matcher = &matcher }},
		{"changed timeout", func(h *codexproto.HookMetadata) { h.TimeoutSec = 60 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.mutate(&changed)
			response := codexproto.HooksListResponse{Data: []codexproto.HooksListEntry{{Hooks: []codexproto.HookMetadata{base, changed}}}}
			states := trustedTeamHooks(response, map[string]string{"Stop": command})
			if len(states) != 1 {
				t.Fatalf("unexpected trusted hook states: %#v", states)
			}
			response.Data[0].Hooks = []codexproto.HookMetadata{changed}
			if len(trustedTeamHooks(response, map[string]string{"Stop": command})) != 0 {
				t.Fatal("trusted an unvetted hook")
			}
		})
	}
}
