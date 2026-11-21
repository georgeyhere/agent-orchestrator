package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type teamCapture struct {
	path string
	body string
}

func teamServer(t *testing.T, result string) (*httptest.Server, *teamCapture) {
	t.Helper()
	capture := &teamCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/p" {
			_, _ = io.WriteString(w, `{"project":{"id":"p","name":"P"}}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/projects/p/team" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		capture.body = string(body)
		capture.path = r.URL.Path
		_, _ = io.WriteString(w, `{"result":`+result+`}`)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func TestTeamHookSendsLaunchIdentity(t *testing.T) {
	newHookServer := func(t *testing.T, capture *teamCapture) *httptest.Server {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions/worker-1/team-hook" {
				http.NotFound(w, r)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			capture.body = string(body)
			_, _ = io.WriteString(w, `{"result":{}}`)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	t.Run("launch id from environment", func(t *testing.T) {
		cfg := setConfigEnv(t)
		t.Setenv("AO_SESSION_ID", "worker-1")
		t.Setenv("AO_RUNTIME_LAUNCH_ID", "launch-9")
		capture := &teamCapture{}
		writeRunFileFor(t, cfg, newHookServer(t, capture))
		out, _, err := executeCLI(t, Deps{In: strings.NewReader("")}, "team", "hook", "Stop")
		if err != nil {
			t.Fatalf("hook: %v", err)
		}
		var body map[string]string
		if err := json.Unmarshal([]byte(capture.body), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["event"] != "Stop" || body["launchId"] != "launch-9" {
			t.Fatalf("hook body = %q, want event+launchId", capture.body)
		}
		if strings.TrimSpace(out) != "{}" {
			t.Fatalf("hook output = %q", out)
		}
	})
	t.Run("missing launch id omitted", func(t *testing.T) {
		cfg := setConfigEnv(t)
		t.Setenv("AO_SESSION_ID", "worker-1")
		t.Setenv("AO_RUNTIME_LAUNCH_ID", "")
		capture := &teamCapture{}
		writeRunFileFor(t, cfg, newHookServer(t, capture))
		if _, _, err := executeCLI(t, Deps{In: strings.NewReader("")}, "team", "hook", "Stop"); err != nil {
			t.Fatalf("hook: %v", err)
		}
		if strings.Contains(capture.body, "launchId") {
			t.Fatalf("hook body carries empty launchId: %q", capture.body)
		}
	})
}

func TestTeamActPostsSupervisorOperations(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	tests := []struct {
		name      string
		operation string
		arguments string
	}{
		{"direct worker under orchestrator", "agent-add", `{"agent":"direct","role":"worker","parent":"orch"}`},
		{"claude-code worker in chat mode", "agent-add", `{"agent":"claude-worker","role":"worker","parent":"chief","provider":"claude-code","mode":"chat"}`},
		{"direct mission with supervisor", "mission-create", `{"mission":"direct","supervisor":"orch","objective":"Work","criteria":"Verify","workers":["direct"]}`},
		{"legacy chief-led mission", "mission-create", `{"mission":"led","chief":"chief","objective":"Work","criteria":"Verify","workers":["w"]}`},
		{"direct task assign", "task-assign", `{"task":"t","mission":"direct","worker":"direct","objective":"Work"}`},
		{"task submit", "submit", `{"kind":"task","record":"t","revision":1,"result":"Done","evidence":"Tests"}`},
		{"task review", "review", `{"kind":"task","record":"t","revision":2,"approve":true,"feedback":"Verified"}`},
		{"mission signoff", "review", `{"kind":"mission","record":"direct","revision":2,"approve":true,"feedback":"Final signoff"}`},
		{"mission reassign to supervisor", "mission-reassign", `{"mission":"direct","revision":1,"supervisor":"chief","move_workers":true}`},
		{"direct draft", "draft-create", `{"draft":"d","objective":"Work","supervisor":"orch","workers":["direct"]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv, capture := teamServer(t, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)
			out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
				"team", "--project", "p", "act", tc.operation, "--json", tc.arguments)
			if err != nil {
				t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
			}
			if capture.path != "/api/v1/projects/p/team" {
				t.Fatalf("path=%q", capture.path)
			}
			var got struct {
				Operation string         `json:"operation"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal([]byte(capture.body), &got); err != nil {
				t.Fatalf("decode posted body: %v", err)
			}
			if got.Operation != tc.operation {
				t.Fatalf("operation=%q want %q", got.Operation, tc.operation)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(tc.arguments), &want); err != nil {
				t.Fatal(err)
			}
			wantJSON, _ := json.Marshal(want)
			gotJSON, _ := json.Marshal(got.Arguments)
			if string(wantJSON) != string(gotJSON) {
				t.Fatalf("arguments=%s want %s", gotJSON, wantJSON)
			}
			if !strings.Contains(out, `"ok": true`) {
				t.Fatalf("stdout missing daemon result: %q", out)
			}
		})
	}
}

func TestTeamStatusPostsEmptyAction(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, capture := teamServer(t, `{"enabled":true}`)
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"team", "--project", "p", "status")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got struct {
		Operation string         `json:"operation"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(capture.body), &got); err != nil {
		t.Fatalf("decode posted body: %v", err)
	}
	if got.Operation != "status" || len(got.Arguments) != 0 {
		t.Fatalf("body=%s", capture.body)
	}
	if !strings.Contains(out, `"enabled": true`) {
		t.Fatalf("stdout missing daemon result: %q", out)
	}
}

func TestTeamActValidation(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, _ := teamServer(t, `{}`)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	_, _, err := executeCLI(t, deps, "team", "--project", "p", "act", "mission-create", "--json", "nope")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
	_, _, err = executeCLI(t, deps, "team", "--project", "p", "act", "mission-create", "--json", `["array"]`)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
	_, _, err = executeCLI(t, deps, "team", "--project", "p", "act")
	if err == nil {
		t.Fatal("expected an error for a missing operation")
	}
}

func TestTeamActSurfacesDaemonErrors(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/p" {
			_, _ = io.WriteString(w, `{"project":{"id":"p","name":"P"}}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad_request","code":"TEAM_OPERATION_FAILED","message":"Task worker must be explicitly staffed in the mission spec","requestId":"req-1"}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"team", "--project", "p", "act", "task-assign", "--json", `{"task":"t","mission":"m","worker":"w","objective":"Work"}`)
	if err == nil || !strings.Contains(err.Error(), "TEAM_OPERATION_FAILED") ||
		!strings.Contains(err.Error(), "explicitly staffed") {
		t.Fatalf("err=%v", err)
	}
	if ExitCode(err) != 1 {
		t.Fatalf("exit=%d want 1", ExitCode(err))
	}
}
