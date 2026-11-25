package session

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetPRCheckout late-binds the Git capability ClaimPR uses to move the
// claimant's worktree onto the PR head. Nil keeps claims metadata-only.
func (s *Service) SetPRCheckout(c ports.WorkspacePRCheckout) {
	s.prCheckout = c
}

// checkoutClaimedPR moves a single-repo session's worktree onto the claimed
// PR's provider-verified head and reports whether its branch or HEAD changed.
// It is best-effort: the claim has already been recorded, so any refusal or
// failure leaves the worktree as it was and reports false.
func (s *Service) checkoutClaimedPR(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, obs ports.SCMObservation, urls ...string) bool {
	if s.prCheckout == nil {
		return false
	}
	// A workspace project's PR can belong to any of its repositories; picking
	// the child worktree is not supported yet, so those stay metadata-only.
	if project.Kind.WithDefault() != domain.ProjectKindSingleRepo {
		return false
	}
	req, reason := prCheckoutRequest(ctx, rec.Metadata.WorkspacePath, obs, urls)
	if reason == "" {
		res, err := s.prCheckout.CheckoutPullRequest(ctx, req)
		if err != nil {
			s.logClaimCheckout("claim: PR checkout failed", rec.ID, obs, "err", err)
			// A failure after the switch still moved the worktree.
			return res.Changed
		}
		if res.Changed {
			return true
		}
		reason = res.Skipped
	}
	if reason != "" {
		s.logClaimCheckout("claim: PR checkout skipped", rec.ID, obs, "reason", reason)
	}
	return false
}

func (s *Service) logClaimCheckout(msg string, id domain.SessionID, obs ports.SCMObservation, args ...any) {
	if s.logger == nil {
		return
	}
	s.logger.Warn(msg, append([]any{"session", id, "pr", obs.PR.URL}, args...)...)
}

// prCheckoutRequest builds the checkout for obs, or explains why there is none.
func prCheckoutRequest(ctx context.Context, root string, obs ports.SCMObservation, urls []string) (ports.WorkspacePRCheckoutRequest, string) {
	pr := obs.PR
	var headRef string
	switch strings.ToLower(obs.Provider) {
	case "github":
		headRef = "refs/pull/" + strconv.Itoa(pr.Number) + "/head"
	case "gitlab":
		headRef = "refs/merge-requests/" + strconv.Itoa(pr.Number) + "/head"
	default:
		return ports.WorkspacePRCheckoutRequest{}, "provider has no PR head ref"
	}
	if pr.Number <= 0 || strings.TrimSpace(pr.HeadSHA) == "" || strings.TrimSpace(pr.SourceBranch) == "" {
		return ports.WorkspacePRCheckoutRequest{}, "provider did not report the PR head"
	}
	remote := prBaseRemote(gitRemoteURLs(ctx, root), urls)
	if remote == "" {
		return ports.WorkspacePRCheckoutRequest{}, "no remote reaches the PR repository"
	}
	req := ports.WorkspacePRCheckoutRequest{
		Path:           root,
		Remote:         remote,
		HeadRef:        headRef,
		HeadSHA:        pr.HeadSHA,
		Branch:         pr.SourceBranch,
		UpstreamBranch: pr.SourceBranch,
	}
	if prFromFork(obs) {
		// A fork's branch name says nothing about this repository's branches
		// (it is often "main"), and pushing it needs the fork's remote.
		req.Branch = "pr/" + strconv.Itoa(pr.Number)
		req.UpstreamBranch = ""
	}
	return req, ""
}

// prBaseRemote picks the configured remote whose URL is the PR's repository,
// preferring origin, then upstream, then the first by name.
func prBaseRemote(remotes map[string]string, urls []string) string {
	names := make([]string, 0, len(remotes))
	for name := range remotes {
		names = append(names, name)
	}
	rank := func(name string) int {
		switch name {
		case "origin":
			return 0
		case "upstream":
			return 1
		}
		return 2
	}
	sort.Slice(names, func(i, j int) bool {
		if rank(names[i]) != rank(names[j]) {
			return rank(names[i]) < rank(names[j])
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		for _, u := range urls {
			if u != "" && requireSameRepo(u, remotes[name]) == nil {
				return name
			}
		}
	}
	return ""
}

func prFromFork(obs ports.SCMObservation) bool {
	head := strings.TrimSpace(obs.PR.HeadRepo)
	base := strings.TrimSpace(firstNonEmpty(obs.PR.BaseRepo, obs.Repo))
	return head != "" && base != "" && !strings.EqualFold(head, base)
}
