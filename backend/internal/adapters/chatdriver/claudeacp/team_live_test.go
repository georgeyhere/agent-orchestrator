package claudeacp

// Live team-path checks for Claude Chat. Run explicitly with
// AO_LIVE_CLAUDE_ACP=1; each spends a few small real turns against the local
// Claude Code login on fully scratch state (workspace, data dir, and
// CLAUDE_CONFIG_DIR under t.TempDir; the scratch config is seeded with a copy
// of the local login credential, cleaned up with the test).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func seedLiveTeamConfig(t *testing.T, configDir string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home directory unavailable for live credential seeding")
	}
	for _, seed := range [][2]string{
		{filepath.Join(home, ".claude", ".credentials.json"), filepath.Join(configDir, ".credentials.json")},
		{filepath.Join(home, ".claude.json"), filepath.Join(configDir, ".claude.json")},
	} {
		if data, err := os.ReadFile(seed[0]); err == nil {
			if err := os.WriteFile(seed[1], data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func writeLiveTeamSettings(t *testing.T, workspace string, hooks map[string]any) {
	t.Helper()
	dir := filepath.Join(workspace, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.local.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func liveTeamHookCommand(script string, args ...string) map[string]any {
	return map[string]any{
		"type":    "command",
		"command": "powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File \"" + script + "\" " + strings.Join(args, " "),
		"timeout": 30,
	}
}

// Team mailbox hooks must observe the session AND see the daemon route: the
// hook process has to inherit AO_SESSION_ID/AO_DATA_DIR through the ACP
// adapter, or `ao team hook` cannot resolve its agent.
func TestLiveClaudeChatTeamHookSeesDaemonRoute(t *testing.T) {
	if os.Getenv("AO_LIVE_CLAUDE_ACP") != "1" {
		t.Skip("set AO_LIVE_CLAUDE_ACP=1 to run against the local Claude Code account")
	}
	workspace := t.TempDir()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	seedLiveTeamConfig(t, configDir)
	hookLog := filepath.Join(workspace, "hook-log.txt")
	script := filepath.Join(workspace, ".claude", "env-echo.ps1")
	if err := os.MkdirAll(filepath.Join(workspace, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// One script serves both events: it records the event, the daemon route
	// it inherited, and the payload's prompt for attribution.
	program := "param($Event, $Log)\n" +
		"$payload = ($input | Out-String)\n" +
		"Add-Content -Path $Log -Value (\"=== \" + $Event + \" session=\" + $env:AO_SESSION_ID + \" data=\" + $env:AO_DATA_DIR + \"`n\" + $payload.Trim() + \"`n\")\n"
	if err := os.WriteFile(script, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLiveTeamSettings(t, workspace, map[string]any{
		"UserPromptSubmit": []any{map[string]any{"hooks": []any{liveTeamHookCommand(script, "UserPromptSubmit", "\""+hookLog+"\"")}}},
		"Stop":             []any{map[string]any{"hooks": []any{liveTeamHookCommand(script, "Stop", "\""+hookLog+"\"")}}},
	})

	driver := New(claudecode.New(), nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := driver.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, "live-team-route") })
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: domain.SessionID("live-team-route"), DataDir: dataDir,
		WorkspacePath: workspace,
		Env: map[string]string{
			"CLAUDE_CONFIG_DIR": configDir,
			"AO_SESSION_ID":     "live-team-route",
			"AO_DATA_DIR":       dataDir,
			"AO_TEAM_MANAGED":   "1",
		},
		SystemPrompt: "This is an AO team hook check. Keep answers short.",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conversation.(ports.ChatProviderTerminator).Terminate()
	if answer := sendLiveTurn(ctx, t, conversation, "team-route-1", "Reply only with the word ROUTED."); strings.TrimSpace(answer) != "ROUTED" {
		t.Fatalf("answer = %q", answer)
	}
	log, err := os.ReadFile(hookLog)
	if err != nil {
		t.Fatalf("team hooks did not run under ACP: %v", err)
	}
	for _, want := range []string{"=== UserPromptSubmit session=live-team-route", "=== Stop session=live-team-route"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("hook log missing %q:\n%s", want, log)
		}
	}
	if !strings.Contains(string(log), "data="+dataDir) {
		t.Fatalf("hook children missed AO_DATA_DIR:\n%s", log)
	}
	if !strings.Contains(string(log), `"prompt":"Reply only with the word ROUTED."`) {
		t.Fatalf("UserPromptSubmit payload missing prompt:\n%s", log)
	}
}

// An interrupted ACP turn must leave the session usable (the next prompt is
// the idle wake) and a resumed session must deliver each prompt exactly once:
// no replayed or duplicated turn execution across Hibernate+Resume.
func TestLiveClaudeChatWakeAfterInterruptAndResumeOnce(t *testing.T) {
	if os.Getenv("AO_LIVE_CLAUDE_ACP") != "1" {
		t.Skip("set AO_LIVE_CLAUDE_ACP=1 to run against the local Claude Code account")
	}
	workspace := t.TempDir()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	seedLiveTeamConfig(t, configDir)
	hookLog := filepath.Join(workspace, "hook-log.txt")
	script := filepath.Join(workspace, ".claude", "submit-log.ps1")
	if err := os.MkdirAll(filepath.Join(workspace, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	program := "param($Log)\n$payload = ($input | Out-String)\nAdd-Content -Path $Log -Value ($payload.Trim() + \"`n\")\n"
	if err := os.WriteFile(script, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLiveTeamSettings(t, workspace, map[string]any{
		"UserPromptSubmit": []any{map[string]any{"hooks": []any{liveTeamHookCommand(script, "\""+hookLog+"\"")}}},
	})

	driver := New(claudecode.New(), nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := driver.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, "live-team-wake") })
	env := map[string]string{"CLAUDE_CONFIG_DIR": configDir}
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: domain.SessionID("live-team-wake"), DataDir: dataDir,
		WorkspacePath: workspace, Env: env,
		SystemPrompt: "This is an AO team hook check. Keep answers short.",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conversation.(ports.ChatProviderTerminator).Terminate()

	ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
		Text: "List the numbers from 1 to 40, one per line, each with a short sentence.", ClientMessageID: "team-wake-long", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conversation.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	interrupted := false
	var state domain.TurnState
	var failure domain.ConversationFailureCode
	var answer strings.Builder
drain:
	for {
		select {
		case event, ok := <-conversation.Events():
			if !ok {
				t.Fatal("controller closed during interrupt turn")
			}
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				answer.WriteString(event.Delta)
				// Interrupt only once provider text is flowing: interrupting
				// on an empty early delta races turn start and fails it.
				if !interrupted && answer.String() != "" {
					interrupted = true
					if err := conversation.Interrupt(ctx, ref.ProviderTurnID); err != nil {
						t.Fatalf("Interrupt: %v", err)
					}
				}
			case ports.ChatEventTurnCompleted:
				state, failure = event.TurnState, event.FailureCode
				// The production controller acknowledges every turn; without
				// the ack the bridge refuses subsequent prompts.
				if acknowledger, ok := conversation.(ports.ChatProviderEventAcknowledger); ok && event.ProviderEventID != "" {
					if err := acknowledger.AcknowledgeProviderEvent(ctx, event.ProviderEventID); err != nil {
						t.Logf("ack interrupted turn: %v", err)
					}
				}
				break drain
			}
		case <-ctx.Done():
			t.Fatalf("interrupt turn timed out: %v", ctx.Err())
		}
	}
	if !interrupted {
		t.Fatal("interrupt turn produced no output to interrupt on")
	}
	// An interrupt lands as interrupted once the provider acknowledges the
	// cancel; a cancel that races provider shutdown surfaces as failed or
	// cancelled instead. Either way the turn must end and the session that
	// follows it must work: that follow-up turn is the asserted property.
	t.Logf("interrupted turn settled state=%s failure=%q", state, string(failure))
	switch state {
	case domain.TurnStateInterrupted, domain.TurnStateCancelled, domain.TurnStateFailed:
	default:
		t.Fatalf("turn state = %q, want an interrupt outcome", state)
	}
	if answer := sendLiveTurn(ctx, t, conversation, "team-wake-1", "Reply only with the word AWAKE."); strings.TrimSpace(answer) != "AWAKE" {
		t.Fatalf("post-interrupt answer = %q", answer)
	}

	providerID := conversation.ProviderConversationID()
	if err := conversation.(ports.ChatProviderHibernator).Hibernate(); err != nil {
		t.Fatalf("Hibernate: %v", err)
	}
	conversation, err = driver.Resume(ctx, ports.ChatResumeConfig{
		SessionID: domain.SessionID("live-team-wake"), ProviderConversationID: providerID,
		DataDir: dataDir, WorkspacePath: workspace, Env: env,
		SystemPrompt: "This is an AO team hook check. Keep answers short.",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if conversation.ProviderConversationID() != providerID {
		t.Fatal("resume did not rejoin the same provider session")
	}
	const resumePrompt = "Reply only with the word DELIVEREDONCE."
	if answer := sendLiveTurn(ctx, t, conversation, "team-wake-2", resumePrompt); strings.TrimSpace(answer) != "DELIVEREDONCE" {
		t.Fatalf("resumed answer = %q", answer)
	}
	log, err := os.ReadFile(hookLog)
	if err != nil {
		t.Fatalf("submit hook never ran: %v", err)
	}
	if got := strings.Count(string(log), resumePrompt); got != 1 {
		t.Fatalf("resumed prompt submitted %d times, want exactly once", got)
	}
}

// A Stop hook decision must extend an ACP turn the way it extends a TUI turn:
// the turn continues while the hook blocks and ends once it releases. This is
// the mechanism team Stop-blocking (pending mail) relies on.
func TestLiveClaudeChatStopBlockExtendsTurn(t *testing.T) {
	if os.Getenv("AO_LIVE_CLAUDE_ACP") != "1" {
		t.Skip("set AO_LIVE_CLAUDE_ACP=1 to run against the local Claude Code account")
	}
	workspace := t.TempDir()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	seedLiveTeamConfig(t, configDir)
	hookLog := filepath.Join(workspace, "hook-log.txt")
	counter := filepath.Join(workspace, "stop-count.txt")
	script := filepath.Join(workspace, ".claude", "stop-counter.ps1")
	if err := os.MkdirAll(filepath.Join(workspace, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Block the first Stop, then release: pending mail, then delivery+ack.
	// Each firing records its own decision so the log shows the sequence.
	program := "param($Log, $Counter)\n" +
		"$n = 0\nif (Test-Path $Counter) { $n = [int](Get-Content $Counter) }\n$n++\n" +
		"Set-Content -Path $Counter -Value $n\n" +
		"$decision = \"release\"\nif ($n -eq 1) { $decision = \"block\" }\n" +
		"Add-Content -Path $Log -Value (\"=== Stop n=\" + $n + \" decision=\" + $decision + \" ===`n\")\n" +
		"if ($n -eq 1) { Write-Output '{\"decision\":\"block\",\"reason\":\"PROBE-BLOCK-1\"}' }\n"
	if err := os.WriteFile(script, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLiveTeamSettings(t, workspace, map[string]any{
		"Stop": []any{map[string]any{"hooks": []any{liveTeamHookCommand(script, "\""+hookLog+"\"", "\""+counter+"\"")}}},
	})

	driver := New(claudecode.New(), nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := driver.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, "live-team-block") })
	conversation, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: domain.SessionID("live-team-block"), DataDir: dataDir,
		WorkspacePath: workspace,
		Env:           map[string]string{"CLAUDE_CONFIG_DIR": configDir},
		SystemPrompt:  "This is an AO team hook check. Keep answers short.",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conversation.(ports.ChatProviderTerminator).Terminate()
	// Warm-up turn: a cold hook runtime can deliver the block decision too
	// slowly on the very first stop, which ends the turn without extension.
	// (Its own first stop blocks too; only completion matters here.)
	if warm := sendLiveTurn(ctx, t, conversation, "team-block-0", "Reply only with the word WARM."); !strings.Contains(warm, "WARM") {
		t.Fatalf("warm-up answer = %q", warm)
	}
	if err := os.Remove(counter); err != nil {
		t.Fatalf("reset stop counter: %v", err)
	}
	answer := sendLiveTurn(ctx, t, conversation, "team-block-1", "Reply only with the word HELD.")
	raw, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("Stop hook never ran: %v", err)
	}
	// The first Stop blocked, so the turn must continue into a second Stop
	// that releases it. The counter is the blocking signal; the answer only
	// has to show the turn completed normally afterward.
	if strings.TrimSpace(string(raw)) != "2" {
		t.Fatalf("Stop hook ran %s times, want exactly 2 (block then release); answer=%q", strings.TrimSpace(string(raw)), answer)
	}
	if !strings.Contains(answer, "HELD") {
		t.Fatalf("answer missing HELD: %q", answer)
	}
}
