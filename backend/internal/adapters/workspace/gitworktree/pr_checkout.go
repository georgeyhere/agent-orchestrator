package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// prCheckoutFetchBudget bounds the one network call a PR checkout makes. A
// claim waits on it, so a slow remote must not hold the request open.
const prCheckoutFetchBudget = 60 * time.Second

// prCheckoutRefPrefix holds the fetched PR head while it is verified. Keying
// it by the verified SHA keeps concurrent claims of different PRs apart and
// keeps the checkout off FETCH_HEAD, which a branch of that name can shadow.
const prCheckoutRefPrefix = "refs/ao/pr-checkout/"

// Skip reasons reported by CheckoutPullRequest.
const (
	prCheckoutSkipDirty     = "worktree has uncommitted changes"
	prCheckoutSkipDetached  = "HEAD is detached; switching could strand its commits"
	prCheckoutSkipHeadMoved = "PR head moved since it was observed"
	prCheckoutSkipDiverged  = "local branch has commits that are not in the PR"
	prCheckoutSkipElsewhere = "branch is checked out in another worktree"
)

var _ ports.WorkspacePRCheckout = (*Workspace)(nil)

// CheckoutPullRequest fetches a PR head into a private ref, verifies it is
// exactly req.HeadSHA, and switches the worktree to req.Branch at that commit.
// Every condition that could lose work leaves the worktree untouched and is
// reported as Skipped.
func (w *Workspace) CheckoutPullRequest(ctx context.Context, req ports.WorkspacePRCheckoutRequest) (ports.WorkspacePRCheckoutResult, error) {
	path, err := w.validateManagedPath(strings.TrimSpace(req.Path))
	if err != nil {
		return ports.WorkspacePRCheckoutResult{}, err
	}
	sha := strings.ToLower(strings.TrimSpace(req.HeadSHA))
	if !isFullObjectID(sha) {
		return ports.WorkspacePRCheckoutResult{}, fmt.Errorf("gitworktree: PR checkout needs a full head SHA, got %q", req.HeadSHA)
	}
	if !strings.HasPrefix(req.HeadRef, "refs/") || strings.ContainsAny(req.HeadRef, ": \t\n") {
		return ports.WorkspacePRCheckoutResult{}, fmt.Errorf("gitworktree: invalid PR head ref %q", req.HeadRef)
	}
	if err := w.validateBranch(ctx, path, req.Branch); err != nil {
		return ports.WorkspacePRCheckoutResult{}, err
	}
	if req.UpstreamBranch != "" {
		if err := w.validateBranch(ctx, path, req.UpstreamBranch); err != nil {
			return ports.WorkspacePRCheckoutResult{}, err
		}
	}
	if ok, err := w.remoteExists(ctx, path, req.Remote); err != nil {
		return ports.WorkspacePRCheckoutResult{}, err
	} else if !ok {
		return ports.WorkspacePRCheckoutResult{}, fmt.Errorf("gitworktree: remote %q is not configured", req.Remote)
	}

	current, head, err := w.currentBranchAndHead(ctx, path)
	if err != nil {
		return ports.WorkspacePRCheckoutResult{}, err
	}
	if current == req.Branch && head == sha {
		return ports.WorkspacePRCheckoutResult{Branch: current}, nil
	}
	skip := func(reason string) (ports.WorkspacePRCheckoutResult, error) {
		return ports.WorkspacePRCheckoutResult{Branch: current, Skipped: reason}, nil
	}
	if dirty, err := w.isDirty(ctx, path); err != nil {
		return ports.WorkspacePRCheckoutResult{}, err
	} else if dirty {
		return skip(prCheckoutSkipDirty)
	}
	if current == "" && head != sha {
		return skip(prCheckoutSkipDetached)
	}

	private := prCheckoutRefPrefix + sha
	refspecs := []string{"+" + req.HeadRef + ":" + private}
	if req.UpstreamBranch != "" {
		refspecs = append(refspecs, "+refs/heads/"+req.UpstreamBranch+":refs/remotes/"+req.Remote+"/"+req.UpstreamBranch)
	}
	fetchCtx, cancel := context.WithTimeout(ctx, prCheckoutFetchBudget)
	_, err = w.run(fetchCtx, w.binary, append([]string{"-C", path, "fetch", "--no-tags", "--", req.Remote}, refspecs...)...)
	cancel()
	// The private ref only carries the fetch to the verification below.
	defer func() {
		_, _ = w.run(context.WithoutCancel(ctx), w.binary, "-C", path, "update-ref", "-d", private)
	}()
	if err != nil {
		return ports.WorkspacePRCheckoutResult{}, fmt.Errorf("gitworktree: fetch PR head: %w", err)
	}
	fetched, err := w.revParse(ctx, path, private+"^{commit}")
	if err != nil {
		return ports.WorkspacePRCheckoutResult{}, err
	}
	if fetched != sha {
		return skip(prCheckoutSkipHeadMoved)
	}

	records, err := w.listRecords(ctx, path)
	if err != nil {
		return ports.WorkspacePRCheckoutResult{}, err
	}
	for _, rec := range records {
		if rec.Branch == req.Branch && !samePath(rec.Path, path) {
			return skip(prCheckoutSkipElsewhere)
		}
	}

	if reason, err := w.movePRBranch(ctx, path, req.Branch, current, sha); err != nil {
		return ports.WorkspacePRCheckoutResult{}, err
	} else if reason != "" {
		return skip(reason)
	}

	if req.UpstreamBranch != "" {
		// Tracking only makes a plain push target the PR branch. Missing it is
		// not a failed checkout, so the worktree still reports the switch.
		upstream := req.Remote + "/" + req.UpstreamBranch
		if _, err := w.run(ctx, w.binary, "-C", path, "branch", "--set-upstream-to="+upstream, req.Branch); err != nil && w.logger != nil {
			w.logger.Warn("pr checkout: set upstream failed", "path", path, "branch", req.Branch, "upstream", upstream, "err", err)
		}
	}
	after, afterHead, err := w.currentBranchAndHead(ctx, path)
	if err != nil {
		return ports.WorkspacePRCheckoutResult{}, err
	}
	if after != req.Branch || afterHead != sha {
		return ports.WorkspacePRCheckoutResult{Changed: after != current || afterHead != head, Branch: after}, fmt.Errorf("gitworktree: PR checkout ended on %q at %s, want %q at %s", after, afterHead, req.Branch, sha)
	}
	return ports.WorkspacePRCheckoutResult{Changed: true, Branch: after}, nil
}

// movePRBranch points branch at sha and checks it out in path. It only ever
// creates the branch or fast-forwards it, so no commit becomes unreachable.
func (w *Workspace) movePRBranch(ctx context.Context, path, branch, current, sha string) (string, error) {
	local := "refs/heads/" + branch
	exists, err := w.refExists(ctx, path, local)
	if err != nil {
		return "", err
	}
	if !exists {
		if _, err := w.run(ctx, w.binary, "-C", path, "switch", "--no-guess", "-c", branch, sha); err != nil {
			return "", fmt.Errorf("gitworktree: create %q at PR head: %w", branch, err)
		}
		return "", nil
	}
	tip, err := w.revParse(ctx, path, local)
	if err != nil {
		return "", err
	}
	if tip != sha {
		ancestor, err := w.isAncestor(ctx, path, tip, sha)
		if err != nil {
			return "", err
		}
		if !ancestor {
			return prCheckoutSkipDiverged, nil
		}
		if current == branch {
			// The worktree is clean, so this only moves HEAD forward.
			if _, err := w.run(ctx, w.binary, "-C", path, "merge", "--ff-only", sha); err != nil {
				return "", fmt.Errorf("gitworktree: fast-forward %q: %w", branch, err)
			}
			return "", nil
		}
		// Compare-and-swap, so a commit that lands on the branch meanwhile
		// keeps it where it is and fails the checkout instead.
		if _, err := w.run(ctx, w.binary, "-C", path, "update-ref", local, sha, tip); err != nil {
			return "", fmt.Errorf("gitworktree: fast-forward %q: %w", branch, err)
		}
	}
	if current != branch {
		if _, err := w.run(ctx, w.binary, "-C", path, "switch", "--no-guess", branch); err != nil {
			return "", fmt.Errorf("gitworktree: switch to %q: %w", branch, err)
		}
	}
	return "", nil
}

func (w *Workspace) currentBranchAndHead(ctx context.Context, path string) (string, string, error) {
	out, err := w.run(ctx, w.binary, "-C", path, "branch", "--show-current")
	if err != nil {
		return "", "", fmt.Errorf("gitworktree: read current branch: %w", err)
	}
	head, err := w.revParse(ctx, path, "HEAD")
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(out)), head, nil
}

func (w *Workspace) isAncestor(ctx context.Context, path, ancestor, descendant string) (bool, error) {
	_, err := w.run(ctx, w.binary, "-C", path, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("gitworktree: ancestry of %s: %w", ancestor, err)
}

func samePath(a, b string) bool {
	if pa, err := physicalAbs(a); err == nil {
		a = pa
	}
	if pb, err := physicalAbs(b); err == nil {
		b = pb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func isFullObjectID(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	for _, r := range sha {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
