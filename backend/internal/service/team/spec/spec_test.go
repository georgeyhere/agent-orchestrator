package spec

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func repo() fstest.MapFS {
	return fstest.MapFS{
		"internal/auth/middleware.go": {Data: []byte("package auth")},
		"internal/auth/token.go":      {Data: []byte("package auth")},
		"docs/adr/0007-token-auth.md": {Data: []byte("# ADR")},
	}
}

func example(t *testing.T) *Brief {
	t.Helper()
	f, err := os.Open("testdata/token-auth.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := DecodeBrief(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fields(findings []Finding) []string {
	out := make([]string, len(findings))
	for i, f := range findings {
		out[i] = string(f.Severity) + " " + f.Field
	}
	return out
}

func requireFinding(t *testing.T, findings []Finding, sev Severity, field, contains string) {
	t.Helper()
	for _, f := range findings {
		if f.Severity == sev && f.Field == field && strings.Contains(f.Message, contains) {
			return
		}
	}
	t.Fatalf("missing %s finding on %s containing %q; got:\n%s", sev, field, contains, strings.Join(fields(findings), "\n"))
}

func TestWorkedExampleLintsClean(t *testing.T) {
	findings := Lint(example(t), LintOptions{Files: repo()})
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got:\n%v", findings)
	}
}

func TestDecodeJSONAndYAMLAgree(t *testing.T) {
	b := example(t)
	if b.Tasks[0].ReadFirst[0].Symbol != "Authenticate" || *b.Acceptance[0].Expect.Exit != 0 {
		t.Fatalf("yaml decoded unexpectedly: %+v", b.Tasks[0].ReadFirst[0])
	}
	j, err := DecodeBrief(strings.NewReader(`{"goal":"g","non_goals":["n"],"acceptance":[],"tasks":[],"staffing":{"supervisor":"s"}}`))
	if err != nil || j.Goal != "g" || j.Staffing.Supervisor != "s" {
		t.Fatalf("json decode: %+v %v", j, err)
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	for name, doc := range map[string]string{
		"json": `{"goal":"g","acceptance_criteria":[]}`,
		"yaml": "goal: g\nacceptance_criteria: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeBrief(strings.NewReader(doc)); err == nil {
				t.Fatal("expected unknown field error")
			}
		})
	}
	if _, err := DecodeBrief(strings.NewReader("  ")); err == nil {
		t.Fatal("expected empty document error")
	}
}

func TestLintRequiredFieldsNameTheirField(t *testing.T) {
	b := &Brief{Tasks: []WorkOrder{{ID: "a"}}}
	findings := Lint(b, LintOptions{})
	for _, want := range []string{
		"goal", "non_goals", "acceptance", "staffing.supervisor",
		"tasks[a].title", "tasks[a].outcome", "tasks[a].read_first", "tasks[a].owns",
		"tasks[a].steps", "tasks[a].do_not", "tasks[a].checks", "tasks[a].evidence",
		"tasks[a].escalate_if", "tasks[a].budget",
	} {
		requireFinding(t, findings, SeverityError, want, "")
	}
	requireFinding(t, findings, SeverityWarning, "decisions", "")
	if !Blocking(findings) {
		t.Fatal("missing required fields must block release")
	}
}

func TestLintPathsAtBaseCommit(t *testing.T) {
	b := example(t)
	b.Tasks[0].ReadFirst[1].Path = "internal/auth/tokens.go"
	b.Tasks[0].Owns = append(b.Tasks[0].Owns, "internal/authz/new.go", "internal/auth/new_file.go", "internal/*/x.go", "/etc/passwd", "../out.go")
	findings := Lint(b, LintOptions{Files: repo()})
	requireFinding(t, findings, SeverityError, "tasks[token-parser].read_first[1].path", "does not exist")
	requireFinding(t, findings, SeverityError, "tasks[token-parser].owns[2]", "internal/authz does not exist")
	requireFinding(t, findings, SeverityError, "tasks[token-parser].owns[5]", "relative")
	requireFinding(t, findings, SeverityError, "tasks[token-parser].owns[6]", "..")
	for _, f := range findings {
		if f.Field == "tasks[token-parser].owns[3]" || f.Field == "tasks[token-parser].owns[4]" {
			t.Fatalf("new file in an existing directory and a glob over existing dirs are allowed: %v", f)
		}
	}
	if got := Lint(b, LintOptions{}); hasField(got, "tasks[token-parser].read_first[1].path") {
		t.Fatal("path existence is skipped without Files")
	}
}

func hasField(findings []Finding, field string) bool {
	for _, f := range findings {
		if f.Field == field {
			return true
		}
	}
	return false
}

func TestLintUnresolvedWording(t *testing.T) {
	b := example(t)
	b.Tasks[0].Steps[1] = "Figure out where the header is parsed"
	b.Tasks[0].Steps[2] = "Handle errors as appropriate, logging etc."
	b.Tasks[0].EscalateIf[0] = "TBD"
	findings := Lint(b, LintOptions{})
	requireFinding(t, findings, SeverityError, "tasks[token-parser].steps[2]", "Figure out")
	requireFinding(t, findings, SeverityError, "tasks[token-parser].steps[3]", "as appropriate")
	requireFinding(t, findings, SeverityError, "tasks[token-parser].escalate_if[0]", "TBD")
	b = example(t)
	b.Tasks[0].Steps[0] = "Set the etcd client timeout and the Todoist field"
	if hasField(Lint(b, LintOptions{}), "tasks[token-parser].steps[1]") {
		t.Fatal("words containing etc or todo are not unresolved wording")
	}
}

func TestLintStepCountWarns(t *testing.T) {
	b := example(t)
	b.Tasks[0].Steps = b.Tasks[0].Steps[:2]
	findings := Lint(b, LintOptions{})
	requireFinding(t, findings, SeverityWarning, "tasks[token-parser].steps", "has 2 steps")
	if Blocking(findings) {
		t.Fatalf("step count alone must not block: %v", findings)
	}
}

func TestLintChecks(t *testing.T) {
	b := example(t)
	b.Tasks[0].Checks = []Check{
		{Kind: CheckRun, Command: `go test "./internal/auth/...`, Expect: &Expect{}},
		{Kind: CheckPRCI, Require: "mostly"},
		{Kind: CheckReviewer},
		{Command: "go vet ./..."},
	}
	findings := Lint(b, LintOptions{})
	requireFinding(t, findings, SeverityError, "tasks[token-parser].checks[0].command", "unterminated")
	requireFinding(t, findings, SeverityError, "tasks[token-parser].checks[0].expect", "required")
	requireFinding(t, findings, SeverityError, "tasks[token-parser].checks[1].require", "green")
	requireFinding(t, findings, SeverityError, "tasks[token-parser].checks[2].role", "required")
	requireFinding(t, findings, SeverityError, "tasks[token-parser].checks[3].kind", "required")

	b = example(t)
	b.Tasks[0].Checks = []Check{{Kind: CheckPRCI, Require: RequireGreen}}
	requireFinding(t, Lint(b, LintOptions{}), SeverityError, "tasks[token-parser].checks", "at least one run check")
}

func TestParseCommand(t *testing.T) {
	for cmd, ok := range map[string]bool{
		"go test ./...":                 true,
		`sh -c 'echo "hi"'`:             true,
		`grep -q "a b" file`:            true,
		`echo it\'s`:                    true,
		`echo 'unterminated`:            false,
		`echo trailing\`:                false,
		"   ":                           false,
		`printf "%s\n" "x"`:             true,
		`python -c "print(\"quoted\")"`: true,
	} {
		if err := parseCommand(cmd); (err == nil) != ok {
			t.Errorf("parseCommand(%q) = %v, want ok=%v", cmd, err, ok)
		}
	}
}

func TestLintGraph(t *testing.T) {
	b := example(t)
	b.Tasks[0].DependsOn = []string{"cookie-removal"}
	requireFinding(t, Lint(b, LintOptions{}), SeverityError, "tasks[token-parser].depends_on", "cycle: token-parser -> cookie-removal -> token-parser")

	b = example(t)
	b.Tasks[1].DependsOn = []string{"token-parser", "nope", "cookie-removal"}
	findings := Lint(b, LintOptions{})
	requireFinding(t, findings, SeverityError, "tasks[cookie-removal].depends_on[1]", "unknown task")
	requireFinding(t, findings, SeverityError, "tasks[cookie-removal].depends_on[2]", "itself")

	b = example(t)
	b.Tasks[1].ID = "token-parser"
	requireFinding(t, Lint(b, LintOptions{}), SeverityError, "tasks[1].id", "duplicates")
}

func TestLintOwnershipOverlapOnlyForConcurrentTasks(t *testing.T) {
	b := example(t)
	if hasField(Lint(b, LintOptions{}), "tasks[cookie-removal].owns") {
		t.Fatal("ordered tasks may own the same files")
	}
	b.Tasks[1].DependsOn = nil
	requireFinding(t, Lint(b, LintOptions{}), SeverityError, "tasks[cookie-removal].owns", "can run at the same time")

	// Transitive ordering also serialises tasks.
	third := b.Tasks[1]
	third.ID = "docs"
	third.Owns = []string{"internal/auth/"}
	third.DependsOn = []string{"cookie-removal"}
	b = example(t)
	b.Tasks = append(b.Tasks, third)
	if hasField(Lint(b, LintOptions{}), "tasks[docs].owns") {
		t.Fatal("a transitive dependency orders the tasks")
	}
}

func TestGlobsOverlap(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"a/b.go", "a/b.go", true},
		{"a/b.go", "a/c.go", false},
		{"a/*.go", "a/b.go", true},
		{"a/*.go", "a/b_test.go", true},
		{"a/*_test.go", "a/*.md", false},
		{"a/*_test.go", "a/x*.go", true},
		{"a/**", "a/b/c/d.go", true},
		{"a/", "a/b/c.go", true},
		{"a/", "b/c.go", false},
		{"**/*.md", "docs/x.md", true},
		{"**/*.md", "docs/x.go", false},
		{"a/**/z.go", "a/z.go", true},
		{"a/**/z.go", "**/y.go", false},
		{"src/**", "**/gen/*.go", true},
		{"a/b", "a/b/c", false},
	} {
		if got := globsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("globsOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := globsOverlap(tc.b, tc.a); got != tc.want {
			t.Errorf("globsOverlap(%q, %q) = %v, want %v (reversed)", tc.b, tc.a, got, tc.want)
		}
	}
}

func TestLintInterfacesMatch(t *testing.T) {
	b := example(t)
	b.Tasks[1].Interfaces = []Interface{{Name: "token.Verify", Definition: "func Verify(raw string) Claims"}, {Name: "readSessionCookie", Definition: "func readSessionCookie(r *http.Request) string"}}
	findings := Lint(b, LintOptions{})
	requireFinding(t, findings, SeverityError, "tasks[cookie-removal].interfaces[0]", "differs from the brief")
	requireFinding(t, findings, SeverityError, "tasks[cookie-removal].interfaces[0]", "differs from the definition in token-parser")
	requireFinding(t, findings, SeverityWarning, "tasks[cookie-removal].interfaces[1]", "not defined in the brief")
}

func TestLintTiersAndDecisions(t *testing.T) {
	b := example(t)
	b.Staffing.Tiers["worker"] = "medium"
	b.Tasks[0].ModelTier = "huge"
	b.Tasks[0].Decisions = []string{"reuse-verify", "missing"}
	b.Decisions = append(b.Decisions, Decision{ID: "error-code", Choice: "x", Rationale: "y"})
	findings := Lint(b, LintOptions{})
	requireFinding(t, findings, SeverityError, "staffing.tiers.worker", "medium")
	requireFinding(t, findings, SeverityError, "tasks[token-parser].model_tier", "huge")
	requireFinding(t, findings, SeverityError, "tasks[token-parser].decisions[1]", "unknown decision")
	requireFinding(t, findings, SeverityError, "decisions[2].id", "duplicates")
	if example(t).Tasks[1].Tier() != TierCheap {
		t.Fatal("model tier defaults to cheap")
	}
}

func TestLintLegacyTaskWarnsOnly(t *testing.T) {
	b := example(t)
	b.Tasks = append(b.Tasks, LegacyWorkOrder("old", "Fix the login bug\nmore detail"))
	findings := Lint(b, LintOptions{})
	requireFinding(t, findings, SeverityWarning, "tasks[old]", "free-text")
	if Blocking(findings) {
		t.Fatalf("legacy tasks warn instead of blocking: %v", findings)
	}
}

func TestLintWorkOrderAlone(t *testing.T) {
	w := example(t).Tasks[1]
	w.DependsOn = []string{"elsewhere"}
	if findings := LintWorkOrder(w, LintOptions{Files: repo()}); len(findings) != 0 {
		t.Fatalf("graph checks need the brief: %v", findings)
	}
}

func TestRenderWorkOrder(t *testing.T) {
	b := example(t)
	got, err := RenderWorkOrder(b, "token-parser")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "testdata/token-parser.md", got)
	again, _ := RenderWorkOrder(b, "token-parser")
	if again != got {
		t.Fatal("render is not deterministic")
	}
	if strings.Contains(got, "revision") {
		t.Fatal("rendered order must not carry revision numbers")
	}
	dependent, _ := RenderWorkOrder(b, "cookie-removal")
	if !strings.Contains(dependent, "## Already done before this task\n\n- `token-parser`: Requests with a valid bearer token") {
		t.Fatalf("dependent order lists its prerequisites:\n%s", dependent)
	}
	if !strings.Contains(dependent, "**reuse-verify:**") || !strings.Contains(dependent, "**error-code:**") {
		t.Fatal("an order naming no decisions carries all of them")
	}
	if _, err := RenderWorkOrder(b, "nope"); err == nil {
		t.Fatal("unknown task must error")
	}
}

func TestRenderLegacyWorkOrder(t *testing.T) {
	b := &Brief{Goal: "Ship login", Tasks: []WorkOrder{LegacyWorkOrder("old", "Fix the login bug")}}
	got, err := RenderWorkOrder(b, "old")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Work order: Fix the login bug\n\n**Outcome:** Fix the login bug\n\nThis task was written before work orders."
	if !strings.HasPrefix(got, want) || !strings.Contains(got, "## Mission goal\n\nShip login\n") {
		t.Fatalf("legacy render:\n%s", got)
	}
}

func TestRenderBrief(t *testing.T) {
	golden(t, "testdata/token-auth.md", RenderBrief(example(t)))
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1 to create)", err)
	}
	if string(want) != got {
		t.Fatalf("%s differs from render:\n%s", path, got)
	}
}
