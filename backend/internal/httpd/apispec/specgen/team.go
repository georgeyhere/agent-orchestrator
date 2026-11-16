package specgen

import (
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

func teamOperations() []operation {
	responses := []respUnit{{http.StatusOK, controllers.TeamResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}}
	return []operation{
		{method: http.MethodGet, path: "/api/v1/projects/{projectId}/team", id: "getProjectTeam", tag: "projects", summary: "Read project team roles, missions, tasks, and mailbox", pathParams: []any{controllers.TeamProjectParam{}}, resps: responses},
		{method: http.MethodPost, path: "/api/v1/projects/{projectId}/team", id: "applyProjectTeamAction", tag: "projects", summary: "Apply a Python-owned team workflow or mailbox action", pathParams: []any{controllers.TeamProjectParam{}}, reqBody: controllers.TeamRequest{}, resps: responses},
		{method: http.MethodPost, path: "/api/v1/sessions/{id}/team-hook", id: "receiveTeamHook", tag: "sessions", summary: "Deliver pending team mail at an agent lifecycle boundary", pathParams: []any{controllers.TeamHookSessionParam{}}, reqBody: controllers.TeamHookRequest{}, resps: responses},
	}
}
