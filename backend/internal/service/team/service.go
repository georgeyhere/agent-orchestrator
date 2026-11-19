// Package team connects the Python-owned team workflow to AO's native sessions.
package team

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

//go:embed python/aoteam/*.py python/aoteam/protocol.md
var runtimeFiles embed.FS

type Projects interface {
	Get(context.Context, domain.ProjectID) (projectsvc.GetResult, error)
	List(context.Context) ([]projectsvc.Summary, error)
}

// NativeSessions is deliberately narrow so existing session services and fakes
// can supply the same lifecycle boundary without changing the native session kind.
type NativeSessions interface {
	GetRecord(context.Context, domain.SessionID) (domain.SessionRecord, error)
	Get(context.Context, domain.SessionID) (domain.Session, error)
	Spawn(context.Context, ports.SpawnConfig) (domain.Session, int, int, error)
	Send(context.Context, domain.SessionID, string, *ports.SpawnAttachment) error
	ExitAgent(context.Context, domain.SessionID) (sessionsvc.ExitAgentOutcome, error)
	ResumeAgent(context.Context, domain.SessionID) (sessionsvc.ResumeAgentOutcome, error)
}

type Request struct {
	Operation string          `json:"operation"`
	SessionID string          `json:"sessionId,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type Service struct {
	dataDir  string
	projects Projects
	sessions NativeSessions
	log      *slog.Logger
	once     sync.Once
	runtime  string
	err      error
}

func New(dataDir string, projects Projects, sessions NativeSessions, log *slog.Logger) *Service {
	return &Service{dataDir: dataDir, projects: projects, sessions: sessions, log: log}
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*$`)

func (s *Service) statePath(project domain.ProjectID) (string, error) {
	if !safeID.MatchString(string(project)) {
		return "", apierr.Invalid("TEAM_PROJECT_REQUIRED", "A valid project ID is required", nil)
	}
	return filepath.Join(s.dataDir, "teams", string(project)), nil
}

func (s *Service) enabled(project domain.ProjectID) bool {
	path, err := s.statePath(project)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(path, "team.sqlite3"))
	return err == nil
}

func (s *Service) prepareRuntime() (string, error) {
	s.once.Do(func() {
		hash := sha256.New()
		_ = fs.WalkDir(runtimeFiles, "python", func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				data, readErr := runtimeFiles.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				_, _ = hash.Write([]byte(path))
				_, _ = hash.Write(data)
			}
			return nil
		})
		s.runtime = filepath.Join(s.dataDir, "team-runtime", fmt.Sprintf("%x", hash.Sum(nil))[:16])
		s.err = fs.WalkDir(runtimeFiles, "python", func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			data, err := runtimeFiles.ReadFile(path)
			if err != nil {
				return err
			}
			target := filepath.Join(s.runtime, filepath.FromSlash(strings.TrimPrefix(path, "python/")))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o600)
		})
	})
	return s.runtime, s.err
}

func pythonCommand() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("AO_TEAM_PYTHON")); configured != "" {
		return configured, nil
	}
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("team workflow requires Python 3.11+; set AO_TEAM_PYTHON")
}

func (s *Service) call(ctx context.Context, project domain.ProjectID, session, operation string, arguments any) (json.RawMessage, error) {
	state, err := s.statePath(project)
	if err != nil {
		return nil, err
	}
	resolved, err := s.projects.Get(ctx, project)
	if err != nil {
		return nil, err
	}
	if resolved.Project == nil || resolved.Project.FolderMissing {
		return nil, apierr.Invalid("TEAM_PROJECT_UNAVAILABLE", "Project folder and configuration must be available", nil)
	}
	runtime, err := s.prepareRuntime()
	if err != nil {
		return nil, err
	}
	python, err := pythonCommand()
	if err != nil {
		return nil, err
	}
	input := map[string]any{"project_id": project, "project_path": resolved.Project.Path,
		"state_path": state, "session_id": session, "operation": operation, "arguments": arguments}
	if session != "" {
		record, err := s.sessions.GetRecord(ctx, domain.SessionID(session))
		if err != nil {
			return nil, err
		}
		if record.ProjectID != project {
			return nil, apierr.Invalid("TEAM_PROJECT_MISMATCH", "Agent session belongs to a different project", nil)
		}
		input["workspace_path"] = record.Metadata.WorkspacePath
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	command := aoprocess.CommandContext(ctx, python, "-m", "aoteam.runtime")
	aoprocess.ConfigureTreeCancellation(command)
	command.Env = append(os.Environ(), "PYTHONPATH="+runtime, "PYTHONIOENCODING=utf-8", "PYTHONDONTWRITEBYTECODE=1")
	command.Stdin = bytes.NewReader(encoded)
	var output, diagnostic bytes.Buffer
	command.Stdout = &output
	command.Stderr = &diagnostic
	runErr := command.Run()
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("team Python runtime failed: %v %s", runErr, strings.TrimSpace(diagnostic.String()))
	}
	if response.Error != "" {
		return nil, apierr.Invalid("TEAM_OPERATION_FAILED", response.Error, nil)
	}
	if runErr != nil {
		return nil, fmt.Errorf("team Python runtime: %w", runErr)
	}
	return response.Result, nil
}

var publicOperations = map[string]bool{
	"enable": true, "status": true, "export": true, "role-add": true, "agent-add": true,
	"agent-control": true, "mission-create": true, "task-assign": true, "submit": true,
	"review": true, "send": true, "ack": true, "inbox": true, "block": true, "unblock": true,
	"memory-policy": true, "memory-search": true, "memory-read": true, "memory-context": true,
	"memory-related": true, "memory-write": true, "memory-capture": true, "memory-lint": true, "memory-commit": true,
	"memory-recover": true,
	"draft-create":   true, "draft-update": true, "draft-release": true,
	"agent-reparent": true, "mission-reassign": true,
	"mission-staff": true,
}

func (s *Service) Execute(ctx context.Context, project domain.ProjectID, request Request) (json.RawMessage, error) {
	if !publicOperations[request.Operation] {
		return nil, apierr.Invalid("TEAM_OPERATION_UNKNOWN", "Unknown team operation", nil)
	}
	var arguments any = map[string]any{}
	if len(request.Arguments) > 0 {
		if err := json.Unmarshal(request.Arguments, &arguments); err != nil {
			return nil, apierr.Invalid("INVALID_JSON", "Team arguments must be valid JSON", nil)
		}
	}
	result, err := s.call(ctx, project, request.SessionID, request.Operation, arguments)
	if err != nil || request.Operation != "agent-control" {
		return result, err
	}
	var control struct {
		Agent  string `json:"agent"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal(request.Arguments, &control); err != nil {
		return nil, err
	}
	state, err := s.call(ctx, project, "", "status", map[string]any{})
	if err != nil {
		return nil, err
	}
	var snapshot struct {
		Agents []nativeAgent `json:"agents"`
	}
	if err := json.Unmarshal(state, &snapshot); err != nil {
		return nil, err
	}
	for _, agent := range snapshot.Agents {
		if agent.ID != control.Agent || agent.SessionID == nil {
			continue
		}
		id := domain.SessionID(*agent.SessionID)
		if control.Action == "cancel" {
			_, err = s.sessions.ExitAgent(ctx, id)
		}
		if control.Action == "resume" {
			var native domain.Session
			native, err = s.sessions.Get(ctx, id)
			if err == nil && (native.NeedsResume || native.Activity.State == domain.ActivityExited) {
				_, err = s.sessions.ResumeAgent(ctx, id)
			}
		}
	}
	return result, err
}

func (s *Service) resolve(ctx context.Context, project domain.ProjectID, session domain.SessionID) (string, error) {
	record, err := s.sessions.GetRecord(ctx, session)
	if err != nil {
		return "", err
	}
	result, err := s.call(ctx, project, "", "resolve", map[string]any{
		"session_id": session, "request_id": record.ClientRequestID, "orchestrator": record.Kind == domain.KindOrchestrator,
	})
	if err != nil {
		return "", err
	}
	var resolved struct {
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal(result, &resolved); err != nil {
		return "", err
	}
	return resolved.Agent, nil
}

// Instructions is invoked by AO's normal system-prompt builder, including restore.
func (s *Service) Instructions(ctx context.Context, project domain.ProjectID, session domain.SessionID) (string, error) {
	if !s.enabled(project) {
		return "", nil
	}
	agent, err := s.resolve(ctx, project, session)
	if err != nil || agent == "" {
		return "", err
	}
	result, err := s.call(ctx, project, "", "instructions", map[string]any{"agent": agent})
	if err != nil {
		return "", err
	}
	var response struct {
		Text string `json:"text"`
	}
	err = json.Unmarshal(result, &response)
	return response.Text, err
}

func (s *Service) Hook(ctx context.Context, session domain.SessionID, event string) (json.RawMessage, error) {
	record, err := s.sessions.GetRecord(ctx, session)
	if err != nil {
		return nil, err
	}
	if !s.enabled(record.ProjectID) {
		return json.RawMessage(`{}`), nil
	}
	agent, err := s.resolve(ctx, record.ProjectID, session)
	if err != nil || agent == "" {
		return json.RawMessage(`{}`), err
	}
	return s.call(ctx, record.ProjectID, string(session), "hook", map[string]any{"event": event})
}

type nativeAgent struct {
	ID         string             `json:"id"`
	Tier       string             `json:"tier"`
	Parent     string             `json:"parent"`
	Provider   string             `json:"provider"`
	RequestID  string             `json:"request_id"`
	SessionID  *string            `json:"session_id"`
	Mode       domain.SessionMode `json:"mode"`
	NotifiedAt float64            `json:"notified_at"`
}

func (s *Service) dispatchProject(ctx context.Context, project domain.ProjectID) error {
	result, err := s.call(ctx, project, "", "dispatch", map[string]any{})
	if err != nil {
		return err
	}
	var response struct {
		Agents []nativeAgent `json:"agents"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return err
	}
	for _, agent := range response.Agents {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if agent.SessionID == nil {
			kind := domain.KindWorker
			requestID := agent.RequestID
			if agent.Tier == "orchestrator" {
				kind = domain.KindOrchestrator
				requestID = ""
			}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(agent.RequestID)))
			session, _, _, launchErr := s.sessions.Spawn(ctx, ports.SpawnConfig{
				ProjectID: project, Kind: kind, Harness: domain.AgentHarness(agent.Provider),
				ClientRequestID: requestID, ClientRequestHash: hash, RequestedMode: agent.Mode,
				DisplayName: agent.ID + " (" + agent.Tier + ")", DisplayNameExplicit: true,
				Prompt: "Read your project team role, run `ao team inbox`, and handle assigned work. Follow llmwiki and datashard protocols.",
				Async:  true,
			})
			arguments := map[string]any{"agent": agent.ID}
			if launchErr != nil {
				arguments["error"] = launchErr.Error()
			} else {
				arguments["session_id"] = session.ID
			}
			attached, err := s.call(ctx, project, "", "launch-result", arguments)
			if err != nil {
				return err
			}
			var state struct {
				Cancelled bool `json:"cancelled"`
			}
			if err := json.Unmarshal(attached, &state); err != nil {
				return err
			}
			if launchErr == nil && state.Cancelled {
				if _, err := s.sessions.ExitAgent(ctx, session.ID); err != nil {
					return err
				}
			}
			continue
		}
		// Existing busy sessions receive mail through native lifecycle hooks.
		// Idle sessions are woken from durable state, including after daemon restart.
		session, err := s.sessions.Get(ctx, domain.SessionID(*agent.SessionID))
		if err == nil && session.ProvisionState == domain.SessionProvisionFailed {
			message := session.ProvisionError
			if message == "" {
				message = "Native session provisioning failed. Open the session to inspect and retry"
			}
			if _, err := s.call(ctx, project, "", "native-failed", map[string]any{"agent": agent.ID, "error": message}); err != nil {
				return err
			}
			continue
		}
		if err != nil || session.IsTerminated || session.Activity.State != domain.ActivityIdle {
			continue
		}
		if time.Since(time.Unix(int64(agent.NotifiedAt), 0)) < 30*time.Second {
			continue
		}
		wake, err := s.call(ctx, project, "", "wake", map[string]any{"agent": agent.ID})
		if err != nil {
			return err
		}
		var notification struct {
			Needed  bool   `json:"needed"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(wake, &notification); err != nil {
			return err
		}
		if !notification.Needed {
			continue
		}
		if err := s.sessions.Send(ctx, session.ID, notification.Message, nil); err != nil {
			s.log.Warn("team mailbox wake deferred", "agent", agent.ID, "error", err)
			continue
		}
		if _, err := s.call(ctx, project, "", "wake-result", map[string]any{"agent": agent.ID}); err != nil {
			return err
		}
	}
	return nil
}

// Start supervises queued launches and durable mailbox wakeups. AO owns the
// native controllers, process recovery, and conversation persistence throughout.
func (s *Service) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				projects, err := s.projects.List(ctx)
				if err != nil {
					s.log.Warn("team project scan", "error", err)
					continue
				}
				for _, project := range projects {
					if !s.enabled(project.ID) {
						continue
					}
					if err := s.dispatchProject(ctx, project.ID); err != nil && ctx.Err() == nil {
						s.log.Warn("team dispatch deferred", "project", project.ID, "error", err)
					}
				}
			}
		}
	}()
	return done
}
