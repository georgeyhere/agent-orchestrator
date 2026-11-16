package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	teamsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/team"
	"github.com/go-chi/chi/v5"
)

type teamFixture struct {
	project domain.ProjectID
	request teamsvc.Request
	session domain.SessionID
	event   string
}

func (f *teamFixture) Execute(_ context.Context, project domain.ProjectID, request teamsvc.Request) (json.RawMessage, error) {
	f.project = project
	f.request = request
	return json.RawMessage(`{"enabled":true}`), nil
}
func (f *teamFixture) Hook(_ context.Context, session domain.SessionID, event string) (json.RawMessage, error) {
	f.session = session
	f.event = event
	return json.RawMessage(`{"decision":"block","reason":"Pending mail"}`), nil
}

func TestTeamControllerPreservesSessionIdentityAndHookOutput(t *testing.T) {
	fixture := &teamFixture{}
	router := chi.NewRouter()
	(&TeamController{Svc: fixture}).Register(router)
	request := httptest.NewRequest("POST", "/projects/project-one/team", strings.NewReader(`{"operation":"send","sessionId":"worker-1","arguments":{"recipient":"chief","subject":"Done","body":"Evidence"}}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || fixture.project != "project-one" || fixture.request.SessionID != "worker-1" {
		t.Fatalf("request identity lost: %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest("POST", "/sessions/worker-1/team-hook", strings.NewReader(`{"event":"Stop"}`))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || fixture.session != "worker-1" || fixture.event != "Stop" || !strings.Contains(response.Body.String(), `"decision":"block"`) {
		t.Fatalf("hook output lost: %s", response.Body.String())
	}
}

func TestTeamControllerRejectsUnknownEventsAndRequestFields(t *testing.T) {
	router := chi.NewRouter()
	(&TeamController{Svc: &teamFixture{}}).Register(router)
	for _, test := range []struct{ path, body string }{{"/sessions/worker-1/team-hook", `{"event":"Unknown"}`}, {"/projects/project/team", `{"operation":"status","actor":"orchestrator"}`}} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", test.path, strings.NewReader(test.body)))
		if response.Code != 400 {
			t.Fatalf("accepted invalid input: %s", response.Body.String())
		}
	}
}
