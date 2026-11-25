package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakePRCheckout struct {
	reqs []ports.WorkspacePRCheckoutRequest
	res  ports.WorkspacePRCheckoutResult
	err  error
}

func (f *fakePRCheckout) CheckoutPullRequest(_ context.Context, req ports.WorkspacePRCheckoutRequest) (ports.WorkspacePRCheckoutResult, error) {
	f.reqs = append(f.reqs, req)
	return f.res, f.err
}

const claimCheckoutHead = "0123456789abcdef0123456789abcdef01234567"

func claimCheckoutService(t *testing.T, kind domain.ProjectKind, origin, remoteURL string, obs ports.SCMObservation, checkout *fakePRCheckout) (*Service, *fakePRClaimer, string) {
	t.Helper()
	repo := newWorkspaceRepo(t)
	if remoteURL != "" {
		runGit(t, repo, "remote", "add", "upstream", "https://github.com/someone/else.git")
		runGit(t, repo, "remote", "add", "origin", remoteURL)
	}
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: repo, Branch: "ao/mer-1"},
	}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Kind: kind, RepoOriginURL: origin}
	claimer := &fakePRClaimer{}
	svc := NewWithDeps(Deps{Store: st, PRClaimer: claimer, SCM: fakeSCM{obs: obs}})
	if checkout != nil {
		svc.SetPRCheckout(checkout)
	}
	return svc, claimer, repo
}

func githubClaimObservation(headRepo string) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo",
		PR: ports.SCMPRObservation{
			URL: "https://github.com/acme/repo/pull/7", Number: 7,
			SourceBranch: "main", HeadRepo: headRepo, BaseRepo: "acme/repo", HeadSHA: claimCheckoutHead,
		},
	}
}

func TestClaimPRChecksOutPRHead(t *testing.T) {
	for _, tc := range []struct {
		name         string
		obs          ports.SCMObservation
		origin       string
		wantRef      string
		wantBranch   string
		wantUpstream string
	}{
		{
			name: "same repository", obs: githubClaimObservation("acme/repo"), origin: "https://github.com/acme/repo",
			wantRef: "refs/pull/7/head", wantBranch: "main", wantUpstream: "main",
		},
		{
			name: "head repository unknown", obs: githubClaimObservation(""), origin: "https://github.com/acme/repo",
			wantRef: "refs/pull/7/head", wantBranch: "main", wantUpstream: "main",
		},
		{
			name: "fork", obs: githubClaimObservation("contributor/repo"), origin: "https://github.com/acme/repo",
			wantRef: "refs/pull/7/head", wantBranch: "pr/7",
		},
		{
			name: "gitlab merge request",
			obs: ports.SCMObservation{
				Fetched: true, Provider: "gitlab", Host: "gitlab.com", Repo: "group/repo",
				PR: ports.SCMPRObservation{URL: "https://gitlab.com/group/repo/-/merge_requests/7", Number: 7, SourceBranch: "topic", HeadSHA: claimCheckoutHead},
			},
			origin:  "https://gitlab.com/group/repo",
			wantRef: "refs/merge-requests/7/head", wantBranch: "topic", wantUpstream: "topic",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkout := &fakePRCheckout{res: ports.WorkspacePRCheckoutResult{Changed: true, Branch: tc.wantBranch}}
			svc, claimer, repo := claimCheckoutService(t, domain.ProjectKindSingleRepo, tc.origin, tc.origin+".git", tc.obs, checkout)
			res, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !claimer.called || !res.BranchChanged {
				t.Fatalf("claimed=%t result=%+v", claimer.called, res)
			}
			want := ports.WorkspacePRCheckoutRequest{
				Path: repo, Remote: "origin", HeadRef: tc.wantRef, HeadSHA: claimCheckoutHead,
				Branch: tc.wantBranch, UpstreamBranch: tc.wantUpstream,
			}
			if len(checkout.reqs) != 1 || checkout.reqs[0] != want {
				t.Fatalf("checkout requests = %+v, want %+v", checkout.reqs, want)
			}
		})
	}
}

func TestClaimPRKeepsClaimWhenCheckoutDoesNotHappen(t *testing.T) {
	origin := "https://github.com/acme/repo"
	for _, tc := range []struct {
		name       string
		kind       domain.ProjectKind
		remoteURL  string
		obs        func() ports.SCMObservation
		checkout   *fakePRCheckout
		wantCalled bool
	}{
		{name: "no checkout capability", kind: domain.ProjectKindSingleRepo, remoteURL: origin, obs: func() ports.SCMObservation { return githubClaimObservation("acme/repo") }},
		{name: "skipped", kind: domain.ProjectKindSingleRepo, remoteURL: origin, obs: func() ports.SCMObservation { return githubClaimObservation("acme/repo") },
			checkout: &fakePRCheckout{res: ports.WorkspacePRCheckoutResult{Skipped: "worktree has uncommitted changes"}}, wantCalled: true},
		{name: "already on head", kind: domain.ProjectKindSingleRepo, remoteURL: origin, obs: func() ports.SCMObservation { return githubClaimObservation("acme/repo") },
			checkout: &fakePRCheckout{res: ports.WorkspacePRCheckoutResult{Branch: "main"}}, wantCalled: true},
		{name: "failed", kind: domain.ProjectKindSingleRepo, remoteURL: origin, obs: func() ports.SCMObservation { return githubClaimObservation("acme/repo") },
			checkout: &fakePRCheckout{err: errors.New("fetch failed")}, wantCalled: true},
		{name: "workspace project", kind: domain.ProjectKindWorkspace, remoteURL: origin, obs: func() ports.SCMObservation { return githubClaimObservation("acme/repo") },
			checkout: &fakePRCheckout{}},
		{name: "no remote reaches the PR", kind: domain.ProjectKindSingleRepo, obs: func() ports.SCMObservation { return githubClaimObservation("acme/repo") },
			checkout: &fakePRCheckout{}},
		{name: "remote for another repository", kind: domain.ProjectKindSingleRepo, remoteURL: "https://github.com/acme/other.git", obs: func() ports.SCMObservation { return githubClaimObservation("acme/repo") },
			checkout: &fakePRCheckout{}},
		{name: "no provider head", kind: domain.ProjectKindSingleRepo, remoteURL: origin, obs: func() ports.SCMObservation {
			obs := githubClaimObservation("acme/repo")
			obs.PR.HeadSHA = ""
			return obs
		}, checkout: &fakePRCheckout{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, claimer, _ := claimCheckoutService(t, tc.kind, origin, tc.remoteURL, tc.obs(), tc.checkout)
			res, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !claimer.called || res.BranchChanged {
				t.Fatalf("claimed=%t result=%+v", claimer.called, res)
			}
			if tc.checkout != nil && (len(tc.checkout.reqs) > 0) != tc.wantCalled {
				t.Fatalf("checkout requests = %+v, want called %t", tc.checkout.reqs, tc.wantCalled)
			}
		})
	}
}

func TestClaimPRFailedCheckoutAfterSwitchReportsChange(t *testing.T) {
	checkout := &fakePRCheckout{res: ports.WorkspacePRCheckoutResult{Changed: true}, err: errors.New("ended on wrong head")}
	origin := "https://github.com/acme/repo"
	svc, _, _ := claimCheckoutService(t, domain.ProjectKindSingleRepo, origin, origin, githubClaimObservation("acme/repo"), checkout)
	res, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{})
	if err != nil || !res.BranchChanged {
		t.Fatalf("result = %+v, err = %v; a moved worktree must not report unchanged", res, err)
	}
}

// A claim through the real Git adapter lands the session on the PR head,
// fetched from the provider's pull ref, and reports the switch.
func TestClaimPRChecksOutThroughGitAdapter(t *testing.T) {
	base := t.TempDir()
	remote := filepath.Join(base, "remote")
	runGit(t, base, "init", "-b", "main", remote)
	runGit(t, remote, "config", "user.email", "ao@example.com")
	runGit(t, remote, "config", "user.name", "AO Tests")
	runGit(t, remote, "commit", "--allow-empty", "-m", "initial")
	runGit(t, remote, "switch", "-c", "pr-topic")
	runGit(t, remote, "commit", "--allow-empty", "-m", "PR head")
	prHead := strings.TrimSpace(runGit(t, remote, "rev-parse", "HEAD"))
	runGit(t, remote, "update-ref", "refs/pull/7/head", prHead)
	runGit(t, remote, "switch", "main")

	managed := filepath.Join(base, "worktrees")
	worktree := filepath.Join(managed, "mer", "mer-1")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o750); err != nil {
		t.Fatal(err)
	}
	runGit(t, base, "clone", "-q", remote, worktree)
	runGit(t, worktree, "switch", "-c", "ao/mer-1")
	// The remote is named by its provider URL, as a real clone's would be, and
	// git rewrites it to the local fixture so the test makes no network call.
	runGit(t, worktree, "remote", "set-url", "origin", "https://github.com/acme/repo.git")
	runGit(t, worktree, "config", "url."+remote+".insteadOf", "https://github.com/acme/repo.git")
	worktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}

	gitWS, err := gitworktree.New(gitworktree.Options{ManagedRoot: managed, RepoResolver: gitworktree.StaticRepoResolver{"mer": remote}})
	if err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: worktree, Branch: "ao/mer-1"},
	}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	obs := githubClaimObservation("acme/repo")
	obs.PR.SourceBranch, obs.PR.HeadSHA = "pr-topic", prHead
	svc := NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{}, SCM: fakeSCM{obs: obs}})
	svc.SetPRCheckout(gitWS)

	res, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BranchChanged {
		t.Fatalf("result = %+v, want branch changed", res)
	}
	if got := strings.TrimSpace(runGit(t, worktree, "branch", "--show-current")); got != "pr-topic" {
		t.Fatalf("branch = %q", got)
	}
	if got := strings.TrimSpace(runGit(t, worktree, "rev-parse", "HEAD")); got != prHead {
		t.Fatalf("HEAD = %q, want %q", got, prHead)
	}
	if got := strings.TrimSpace(runGit(t, worktree, "rev-parse", "--abbrev-ref", "pr-topic@{upstream}")); got != "origin/pr-topic" {
		t.Fatalf("upstream = %q", got)
	}
}
