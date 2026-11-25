package gitworktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type prCheckoutFixture struct {
	w        *Workspace
	remote   string // the base repository, holding refs/pull/7/head
	worktree string // the session worktree, a clone of remote
	prHead   string
}

func prGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %s: %v\n%s", dir, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newPRCheckoutFixture(t *testing.T) prCheckoutFixture {
	t.Helper()
	base := t.TempDir()
	remote := filepath.Join(base, "remote")
	prGit(t, base, "init", "-b", "main", remote)
	prGit(t, remote, "config", "user.email", "ao@example.com")
	prGit(t, remote, "config", "user.name", "AO Tests")
	prGit(t, remote, "commit", "--allow-empty", "-m", "initial")
	prGit(t, remote, "switch", "-c", "pr-topic")
	if err := os.WriteFile(filepath.Join(remote, "pr.txt"), []byte("PR contents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prGit(t, remote, "add", "pr.txt")
	prGit(t, remote, "commit", "-m", "PR head")
	prHead := prGit(t, remote, "rev-parse", "HEAD")
	prGit(t, remote, "update-ref", "refs/pull/7/head", prHead)
	prGit(t, remote, "switch", "main")

	managed := filepath.Join(base, "worktrees")
	worktree := filepath.Join(managed, "proj", "proj-1")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o750); err != nil {
		t.Fatal(err)
	}
	prGit(t, base, "clone", "-q", remote, worktree)
	prGit(t, worktree, "config", "user.email", "ao@example.com")
	prGit(t, worktree, "config", "user.name", "AO Tests")
	prGit(t, worktree, "switch", "-c", "ao/proj-1")
	root, err := physicalAbs(managed)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err = physicalAbs(worktree)
	if err != nil {
		t.Fatal(err)
	}
	return prCheckoutFixture{
		w:        &Workspace{binary: "git", managedRoot: root, run: runCommand},
		remote:   remote,
		worktree: worktree,
		prHead:   prHead,
	}
}

func (f prCheckoutFixture) request() ports.WorkspacePRCheckoutRequest {
	return ports.WorkspacePRCheckoutRequest{
		Path:           f.worktree,
		Remote:         "origin",
		HeadRef:        "refs/pull/7/head",
		HeadSHA:        f.prHead,
		Branch:         "pr-topic",
		UpstreamBranch: "pr-topic",
	}
}

func (f prCheckoutFixture) assertOn(t *testing.T, branch, head string) {
	t.Helper()
	if got := prGit(t, f.worktree, "branch", "--show-current"); got != branch {
		t.Fatalf("branch = %q, want %q", got, branch)
	}
	if got := prGit(t, f.worktree, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD = %q, want %q", got, head)
	}
	if got := prGit(t, f.worktree, "for-each-ref", prCheckoutRefPrefix); got != "" {
		t.Fatalf("private checkout ref left behind: %s", got)
	}
}

func TestCheckoutPullRequestCreatesTrackingBranchAtVerifiedHead(t *testing.T) {
	f := newPRCheckoutFixture(t)
	// A branch named FETCH_HEAD must not redirect the checkout.
	prGit(t, f.worktree, "branch", "FETCH_HEAD", "main")
	res, err := f.w.CheckoutPullRequest(context.Background(), f.request())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Branch != "pr-topic" || res.Skipped != "" {
		t.Fatalf("result = %+v", res)
	}
	f.assertOn(t, "pr-topic", f.prHead)
	if got := prGit(t, f.worktree, "rev-parse", "--abbrev-ref", "pr-topic@{upstream}"); got != "origin/pr-topic" {
		t.Fatalf("upstream = %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(f.worktree, "pr.txt")); err != nil || string(got) != "PR contents\n" {
		t.Fatalf("pr.txt = %q, err=%v", got, err)
	}
	// The session's own branch survives the switch.
	prGit(t, f.worktree, "rev-parse", "--verify", "refs/heads/ao/proj-1")
}

func TestCheckoutPullRequestForkHeadUsesLocalNameWithoutUpstream(t *testing.T) {
	f := newPRCheckoutFixture(t)
	req := f.request()
	req.Branch, req.UpstreamBranch = "pr/7", ""
	res, err := f.w.CheckoutPullRequest(context.Background(), req)
	if err != nil || !res.Changed {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
	f.assertOn(t, "pr/7", f.prHead)
	cmd := exec.Command("git", "-C", f.worktree, "rev-parse", "--abbrev-ref", "pr/7@{upstream}")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("fork branch has upstream %s", out)
	}
}

func TestCheckoutPullRequestAlreadyOnHead(t *testing.T) {
	f := newPRCheckoutFixture(t)
	prGit(t, f.worktree, "switch", "-c", "pr-topic", f.prHead)
	res, err := f.w.CheckoutPullRequest(context.Background(), f.request())
	if err != nil || res.Changed || res.Skipped != "" || res.Branch != "pr-topic" {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
}

func TestCheckoutPullRequestFastForwardsStaleLocalBranch(t *testing.T) {
	for _, onBranch := range []bool{false, true} {
		f := newPRCheckoutFixture(t)
		stale := prGit(t, f.worktree, "rev-parse", "origin/main")
		prGit(t, f.worktree, "branch", "pr-topic", stale)
		if onBranch {
			prGit(t, f.worktree, "switch", "pr-topic")
		}
		res, err := f.w.CheckoutPullRequest(context.Background(), f.request())
		if err != nil || !res.Changed {
			t.Fatalf("onBranch=%v: result = %+v, err = %v", onBranch, res, err)
		}
		f.assertOn(t, "pr-topic", f.prHead)
	}
}

func TestCheckoutPullRequestLeavesWorktreeWhenUnsafe(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T, f *prCheckoutFixture, req *ports.WorkspacePRCheckoutRequest)
		reason string
	}{
		{"uncommitted change", func(t *testing.T, f *prCheckoutFixture, _ *ports.WorkspacePRCheckoutRequest) {
			if err := os.WriteFile(filepath.Join(f.worktree, "wip.txt"), []byte("wip\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, prCheckoutSkipDirty},
		{"detached HEAD", func(t *testing.T, f *prCheckoutFixture, _ *ports.WorkspacePRCheckoutRequest) {
			prGit(t, f.worktree, "commit", "--allow-empty", "-m", "unreferenced")
			prGit(t, f.worktree, "switch", "--detach")
		}, prCheckoutSkipDetached},
		{"provider head moved", func(t *testing.T, f *prCheckoutFixture, req *ports.WorkspacePRCheckoutRequest) {
			prGit(t, f.remote, "switch", "pr-topic")
			prGit(t, f.remote, "commit", "--allow-empty", "-m", "pushed after observation")
			prGit(t, f.remote, "update-ref", "refs/pull/7/head", "HEAD")
			prGit(t, f.remote, "switch", "main")
		}, prCheckoutSkipHeadMoved},
		{"diverged local branch", func(t *testing.T, f *prCheckoutFixture, _ *ports.WorkspacePRCheckoutRequest) {
			prGit(t, f.worktree, "branch", "pr-topic", "origin/main")
			prGit(t, f.worktree, "switch", "pr-topic")
			prGit(t, f.worktree, "commit", "--allow-empty", "-m", "local only")
			prGit(t, f.worktree, "switch", "ao/proj-1")
		}, prCheckoutSkipDiverged},
		{"branch held by donor worktree", func(t *testing.T, f *prCheckoutFixture, _ *ports.WorkspacePRCheckoutRequest) {
			prGit(t, f.worktree, "fetch", "-q", "origin", "pr-topic")
			donor := filepath.Join(filepath.Dir(f.worktree), "proj-0")
			prGit(t, f.worktree, "worktree", "add", "-q", "-b", "pr-topic", donor, "origin/pr-topic")
		}, prCheckoutSkipElsewhere},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPRCheckoutFixture(t)
			req := f.request()
			tc.setup(t, &f, &req)
			branch := prGit(t, f.worktree, "branch", "--show-current")
			head := prGit(t, f.worktree, "rev-parse", "HEAD")
			status := prGit(t, f.worktree, "status", "--porcelain")
			res, err := f.w.CheckoutPullRequest(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if res.Changed || res.Skipped != tc.reason {
				t.Fatalf("result = %+v, want skipped %q", res, tc.reason)
			}
			f.assertOn(t, branch, head)
			if got := prGit(t, f.worktree, "status", "--porcelain"); got != status {
				t.Fatalf("status = %q, want %q", got, status)
			}
		})
	}
}

func TestCheckoutPullRequestRejectsInvalidRequests(t *testing.T) {
	f := newPRCheckoutFixture(t)
	for name, mutate := range map[string]func(*ports.WorkspacePRCheckoutRequest){
		"abbreviated sha":   func(r *ports.WorkspacePRCheckoutRequest) { r.HeadSHA = r.HeadSHA[:12] },
		"refspec in ref":    func(r *ports.WorkspacePRCheckoutRequest) { r.HeadRef = "refs/pull/7/head:refs/heads/main" },
		"unknown remote":    func(r *ports.WorkspacePRCheckoutRequest) { r.Remote = "nope" },
		"invalid branch":    func(r *ports.WorkspacePRCheckoutRequest) { r.Branch = "bad..name" },
		"outside the root":  func(r *ports.WorkspacePRCheckoutRequest) { r.Path = f.remote },
		"option as remote":  func(r *ports.WorkspacePRCheckoutRequest) { r.Remote = "--upload-pack=touch" },
		"invalid upstream":  func(r *ports.WorkspacePRCheckoutRequest) { r.UpstreamBranch = "bad..name" },
		"non-ref head name": func(r *ports.WorkspacePRCheckoutRequest) { r.HeadRef = "pull/7/head" },
	} {
		t.Run(name, func(t *testing.T) {
			req := f.request()
			mutate(&req)
			if res, err := f.w.CheckoutPullRequest(context.Background(), req); err == nil || res.Changed {
				t.Fatalf("result = %+v, err = %v; want rejection", res, err)
			}
			f.assertOn(t, "ao/proj-1", prGit(t, f.worktree, "rev-parse", "origin/main"))
		})
	}
}
