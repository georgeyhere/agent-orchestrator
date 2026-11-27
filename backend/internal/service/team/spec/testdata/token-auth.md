# Mission brief

**Goal:** API clients can authenticate with bearer tokens instead of session cookies.

## Out of scope

- Issuing or refreshing tokens
- Changing the session cookie format

## Context

- `docs/adr/0007-token-auth.md`: The decision record and the error shapes

## Decisions

- **reuse-verify:** Reuse token.Verify unchanged. It already checks signature and expiry and is covered by tests. Rejected: a second verifier in the middleware.
- **error-code:** Every token failure returns 401 with code TOKEN_INVALID. Clients branch on one code; detail stays in server logs.

## Interfaces

**token.Verify**

```
func Verify(raw string) (Claims, error)
```

## Constraints

- No new dependencies

## Risks

- Verify cannot tell expiry from a bad signature. Signal: The expired-token test needs a different error code. Response: Escalate; do not change token.go

## Acceptance

- auth tests: `go test ./internal/auth/...` must exit 0
- PR CI is green

## Tasks

- `token-parser` (cheap): Parse and validate bearer tokens in auth middleware
- `cookie-removal` (cheap): Remove the session cookie branch from auth middleware. After `token-parser`

## Staffing

- Supervisor: chief-auth
- Workers: worker-1
- planner runs on the strong tier
- worker runs on the cheap tier
