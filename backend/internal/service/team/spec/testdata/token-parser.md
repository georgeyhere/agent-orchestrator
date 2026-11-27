# Work order: Parse and validate bearer tokens in auth middleware

**Outcome:** Requests with a valid bearer token reach handlers with Principal set; expired or malformed tokens get 401 with error code TOKEN_INVALID.

## Mission goal

API clients can authenticate with bearer tokens instead of session cookies.

## Out of scope

- Issuing or refreshing tokens
- Changing the session cookie format

## Decisions already made

Do not reopen these. If one blocks you, escalate.

- **reuse-verify:** Reuse token.Verify unchanged. It already checks signature and expiry and is covered by tests. Rejected: a second verifier in the middleware.
- **error-code:** Every token failure returns 401 with code TOKEN_INVALID. Clients branch on one code; detail stays in server logs.

## Read first

- `internal/auth/middleware.go:Authenticate`: The current cookie path you will extend
- `internal/auth/token.go`: Verify() already exists; reuse it
- `docs/adr/0007-token-auth.md`: Decision and error shapes

## You may change only

- `internal/auth/middleware.go`
- `internal/auth/middleware_test.go`

## Steps

1. In Authenticate, read the Authorization header before the cookie branch.
2. If it starts with "Bearer ", call token.Verify(raw) and set ctx Principal on success.
3. On Verify error, write 401 with {"code":"TOKEN_INVALID"} via httperr.Write.
4. Leave the cookie branch unchanged (task cookie-removal deletes it).
5. Add table tests for valid, expired, malformed, and a missing header that falls through to the cookie.

## Interfaces

Produce or consume these exactly. Do not change them.

**token.Verify**

```
func Verify(raw string) (Claims, error)
```

## Examples

- Given Authorization: Bearer <expired token>, expect 401 {"code":"TOKEN_INVALID"}

## Do not

- edit token.go
- remove cookie support
- add dependencies

## Checks

Run each command through `ao team run` so its result is recorded.

- `go test ./internal/auth/...` must exit 0

## Evidence to submit

- The `ao team run` evidence ID of each check
- The commit SHA

## Stop and escalate if

- Verify() cannot express expiry distinctly from bad signature
- Any change needed outside owns

## Budget

25 turns. When it runs out, stop, block the task and report where you are.
