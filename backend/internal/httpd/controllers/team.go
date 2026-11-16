package controllers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	teamsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/team"
	"github.com/go-chi/chi/v5"
)

type TeamService interface {
	Execute(context.Context, domain.ProjectID, teamsvc.Request) (json.RawMessage, error)
	Hook(context.Context, domain.SessionID, string) (json.RawMessage, error)
}

type TeamProjectParam struct {
	ProjectID string `path:"projectId"`
}
type TeamHookSessionParam struct {
	ID string `path:"id"`
}
type TeamRequest struct {
	Operation string         `json:"operation"`
	SessionID string         `json:"sessionId,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}
type TeamHookRequest struct {
	Event string `json:"event"`
}
type TeamResponse struct {
	Result any `json:"result"`
}

type TeamController struct{ Svc TeamService }

func (c *TeamController) Register(r chi.Router) {
	r.Get("/projects/{projectId}/team", c.status)
	r.Post("/projects/{projectId}/team", c.action)
	r.Post("/sessions/{id}/team-hook", c.hook)
}

func (c *TeamController) available(w http.ResponseWriter, r *http.Request) bool {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, r.Method, "/api/v1/projects/{projectId}/team")
		return false
	}
	return true
}

func (c *TeamController) status(w http.ResponseWriter, r *http.Request) {
	if !c.available(w, r) {
		return
	}
	result, err := c.Svc.Execute(r.Context(), domain.ProjectID(chi.URLParam(r, "projectId")), teamsvc.Request{Operation: "status"})
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "TEAM_STATUS_FAILED", "Unable to read project team")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]json.RawMessage{"result": result})
}

func (c *TeamController) action(w http.ResponseWriter, r *http.Request) {
	if !c.available(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 200000)
	var request TeamRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		envelope.WriteAPIError(w, r, 400, "bad_request", "INVALID_JSON", "Invalid team action", nil)
		return
	}
	arguments, err := json.Marshal(request.Arguments)
	if err != nil {
		envelope.WriteAPIError(w, r, 400, "bad_request", "INVALID_JSON", "Invalid team arguments", nil)
		return
	}
	result, err := c.Svc.Execute(r.Context(), domain.ProjectID(chi.URLParam(r, "projectId")), teamsvc.Request{Operation: request.Operation, SessionID: request.SessionID, Arguments: arguments})
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "TEAM_ACTION_FAILED", "Team action failed")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]json.RawMessage{"result": result})
}

func (c *TeamController) hook(w http.ResponseWriter, r *http.Request) {
	if !c.available(w, r) {
		return
	}
	var request TeamHookRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 10000)).Decode(&request); err != nil {
		envelope.WriteAPIError(w, r, 400, "bad_request", "INVALID_JSON", "Invalid hook event", nil)
		return
	}
	switch request.Event {
	case "SessionStart", "UserPromptSubmit", "PostToolUse", "Stop":
	default:
		envelope.WriteAPIError(w, r, 400, "validation", "TEAM_EVENT_INVALID", "Unknown team hook event", nil)
		return
	}
	result, err := c.Svc.Hook(r.Context(), domain.SessionID(chi.URLParam(r, "id")), request.Event)
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "TEAM_HOOK_FAILED", "Team mailbox hook failed")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]json.RawMessage{"result": result})
}
