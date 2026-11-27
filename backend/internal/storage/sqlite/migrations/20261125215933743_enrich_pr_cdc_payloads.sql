-- +goose Up
-- Carry the stored PR and CI check facts in the pr_created, pr_updated and
-- pr_check_recorded change_log payloads, so live /events consumers see the raw
-- observed facts instead of only the derived state columns (#110). The WHEN
-- clauses are unchanged: this widens what each event says, not when it fires.
-- Internal bookkeeping (observer hashes, nudge signature) and unbounded text
-- (check log tails and details) stay out of the payload.
DROP TRIGGER IF EXISTS pr_cdc_insert;
DROP TRIGGER IF EXISTS pr_cdc_update;
DROP TRIGGER IF EXISTS pr_checks_cdc_insert;
DROP TRIGGER IF EXISTS pr_checks_cdc_update;

-- +goose StatementBegin
CREATE TRIGGER pr_cdc_insert
AFTER INSERT ON pr
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_created',
        json_object(
            'url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
            'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability,
            'autoInjectCI', json(CASE WHEN NEW.auto_inject_ci THEN 'true' ELSE 'false' END),
            'number', NEW.number, 'provider', NEW.provider, 'providerId', NEW.provider_id,
            'host', NEW.host, 'repo', NEW.repo, 'htmlUrl', NEW.html_url, 'title', NEW.title,
            'author', NEW.author, 'sourceBranch', NEW.source_branch, 'targetBranch', NEW.target_branch,
            'headSha', NEW.head_sha, 'baseSha', NEW.base_sha, 'mergeCommitSha', NEW.merge_commit_sha,
            'additions', NEW.additions, 'deletions', NEW.deletions, 'changedFiles', NEW.changed_files,
            'isDraft', json(CASE WHEN NEW.is_draft THEN 'true' ELSE 'false' END),
            'isMerged', json(CASE WHEN NEW.is_merged THEN 'true' ELSE 'false' END),
            'isClosed', json(CASE WHEN NEW.is_closed THEN 'true' ELSE 'false' END),
            'providerState', NEW.provider_state, 'providerMergeable', NEW.provider_mergeable,
            'providerMergeStateStatus', NEW.provider_merge_state_status,
            'reviewPartial', json(CASE WHEN NEW.review_partial THEN 'true' ELSE 'false' END),
            'createdAtProvider', NEW.created_at_provider, 'updatedAtProvider', NEW.updated_at_provider,
            'mergedAtProvider', NEW.merged_at_provider, 'closedAtProvider', NEW.closed_at_provider,
            'stateChangedAt', NEW.state_changed_at, 'observedAt', NEW.observed_at,
            'ciObservedAt', NEW.ci_observed_at, 'reviewObservedAt', NEW.review_observed_at,
            'updatedAt', NEW.updated_at),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER pr_cdc_update
AFTER UPDATE ON pr
WHEN OLD.pr_state <> NEW.pr_state
    OR OLD.ci_state <> NEW.ci_state
    OR OLD.review_decision <> NEW.review_decision
    OR OLD.mergeability <> NEW.mergeability
    OR OLD.auto_inject_ci <> NEW.auto_inject_ci
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_updated',
        json_object(
            'url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
            'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability,
            'autoInjectCI', json(CASE WHEN NEW.auto_inject_ci THEN 'true' ELSE 'false' END),
            'number', NEW.number, 'provider', NEW.provider, 'providerId', NEW.provider_id,
            'host', NEW.host, 'repo', NEW.repo, 'htmlUrl', NEW.html_url, 'title', NEW.title,
            'author', NEW.author, 'sourceBranch', NEW.source_branch, 'targetBranch', NEW.target_branch,
            'headSha', NEW.head_sha, 'baseSha', NEW.base_sha, 'mergeCommitSha', NEW.merge_commit_sha,
            'additions', NEW.additions, 'deletions', NEW.deletions, 'changedFiles', NEW.changed_files,
            'isDraft', json(CASE WHEN NEW.is_draft THEN 'true' ELSE 'false' END),
            'isMerged', json(CASE WHEN NEW.is_merged THEN 'true' ELSE 'false' END),
            'isClosed', json(CASE WHEN NEW.is_closed THEN 'true' ELSE 'false' END),
            'providerState', NEW.provider_state, 'providerMergeable', NEW.provider_mergeable,
            'providerMergeStateStatus', NEW.provider_merge_state_status,
            'reviewPartial', json(CASE WHEN NEW.review_partial THEN 'true' ELSE 'false' END),
            'createdAtProvider', NEW.created_at_provider, 'updatedAtProvider', NEW.updated_at_provider,
            'mergedAtProvider', NEW.merged_at_provider, 'closedAtProvider', NEW.closed_at_provider,
            'stateChangedAt', NEW.state_changed_at, 'observedAt', NEW.observed_at,
            'ciObservedAt', NEW.ci_observed_at, 'reviewObservedAt', NEW.review_observed_at,
            'updatedAt', NEW.updated_at),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER pr_checks_cdc_insert
AFTER INSERT ON pr_checks
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status,
                    'conclusion', NEW.conclusion, 'url', NEW.url, 'createdAt', NEW.created_at),
        NEW.created_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER pr_checks_cdc_update
AFTER UPDATE ON pr_checks
WHEN OLD.status <> NEW.status
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status,
                    'conclusion', NEW.conclusion, 'url', NEW.url, 'createdAt', NEW.created_at),
        datetime('now'));
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS pr_cdc_insert;
DROP TRIGGER IF EXISTS pr_cdc_update;
DROP TRIGGER IF EXISTS pr_checks_cdc_insert;
DROP TRIGGER IF EXISTS pr_checks_cdc_update;

-- +goose StatementBegin
CREATE TRIGGER pr_cdc_insert
AFTER INSERT ON pr
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_created',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER pr_cdc_update
AFTER UPDATE ON pr
WHEN OLD.pr_state <> NEW.pr_state
    OR OLD.ci_state <> NEW.ci_state
    OR OLD.review_decision <> NEW.review_decision
    OR OLD.mergeability <> NEW.mergeability
    OR OLD.auto_inject_ci <> NEW.auto_inject_ci
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_updated',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability,
                    'autoInjectCI', json(CASE WHEN NEW.auto_inject_ci THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER pr_checks_cdc_insert
AFTER INSERT ON pr_checks
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        NEW.created_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER pr_checks_cdc_update
AFTER UPDATE ON pr_checks
WHEN OLD.status <> NEW.status
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        datetime('now'));
END;
-- +goose StatementEnd
