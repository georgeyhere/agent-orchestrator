# Team authority model

Project teams exist so that a Principal can drive less capable agents through
complex work. Weaker agents follow explicit rules more reliably than implied
ones, so every team permission lives in one declarative table,
`backend/internal/service/team/python/aoteam/authority.py`. The runtime enforces
it, agent instructions are generated from it, and `ao team status` returns it
under `authority` for the GUI.

## Actors

| Actor | ID | Who |
| --- | --- | --- |
| Principal | `principal` | The human who owns the project. The desktop GUI and any `ao team` call made outside a managed agent session act as the Principal. |
| System | `system` | The daemon's own bookkeeping, such as launch failures and the "Team agent ready" mail. It holds no team authority. |
| Agent | its team ID | A registered planner, orchestrator, chief, or worker, identified from its native AO session. |

`principal`, `system`, and the legacy `operator` are reserved and cannot be
used as agent IDs. A session attached to an agent that already has one of these
IDs is refused rather than treated as the Principal. Records written before
this change name the human `operator`; read them as the Principal. History is
not rewritten.

`principal-set(name)` stores the Principal's display name (default
`Principal`). The GUI labels decisions with it and agent instructions name it.

## Authority flow

Execution authority flows from the Principal to the orchestrator to
supervisors (chiefs, or the orchestrator on directly supervised missions) to
workers. The planner facilitates planning and has no execution authority. The
Principal's decision takes precedence.

## Rules

A grant is the Principal, a tier ("any orchestrator"), or a relation between
the actor and the records the action touches ("the mission's supervisor").
Operations still apply their own state and hierarchy checks after the rule,
for example revision checks or "a chief may only create workers under itself".

| Action | Meaning | Allowed |
| --- | --- | --- |
| `team.enable` | Enable the project team | the Principal |
| `principal.update` | Change the Principal's display name | the Principal |
| `role.create` | Define a custom role | the Principal, any orchestrator |
| `agent.create` | Add a team agent within your own hierarchy | the Principal, any orchestrator, any chief |
| `agent.control` | Pause, resume, or cancel an agent | the Principal, the agent's direct supervisor |
| `agent.reparent` | Move an idle worker to another supervisor | the Principal, any orchestrator |
| `draft.create` | Start a mission draft | the Principal, any planner, any orchestrator |
| `draft.update` | Revise a mission draft | the Principal, the draft's planner |
| `draft.release` | Release a draft into an active mission | the Principal |
| `mission.create` | Create an active mission directly | the Principal, any orchestrator |
| `mission.staff` | Change a mission's staffed workers | the Principal, the mission's orchestrator |
| `mission.reassign` | Transfer a mission to another supervisor | the Principal, the mission's orchestrator |
| `task.assign` | Assign a task to a staffed worker | the Principal, the mission's supervisor |
| `task.submit` | Submit a task result with evidence | the Principal, the task's worker |
| `task.review` | Accept or return a task submission | the Principal, the mission's supervisor |
| `task.block` | Block or unblock a task | the Principal, the task's worker, the mission's supervisor |
| `mission.submit` | Submit mission results for final signoff | the Principal, the mission's supervisor |
| `mission.review` | Give final mission signoff or request changes | the Principal, the mission's orchestrator |
| `mission.block` | Block or unblock a mission | the Principal, the mission's supervisor, the mission's orchestrator |
| `mail.send` | Send team mail | the Principal, any planner, any orchestrator, any chief, any worker |
| `mail.read` | Read an inbox | the Principal, the inbox owner |
| `mail.ack` | Acknowledge mail you received | the message's recipient |

A test checks that this table lists every rule in `authority.py`.

## Principal decisions

The Principal acts as themself, never as an agent. When the Principal takes an
action whose rule names a specific agent, such as approving a mission whose
signoff belongs to its orchestrator, the runtime:

1. Records the event with actor `principal`, `authority: "principal"`, and
   `in_place_of` listing the agents the rule named.
2. Sends each of those agents a "Principal decision" notice telling it to read
   the current record. Agents that already receive the ordinary notification
   for that action do not get a duplicate.

Agent actions record `authority` as the agent's tier. Denials name who may act
and who the caller is, for example: "Only the Principal, the mission's
supervisor (chief-one) can accept or return a task submission. You are
worker-b, tier worker".

The desktop Missions tab now shows "Approve as {Principal}" and its siblings
instead of "Approve as [agent]". It no longer sends an agent's session ID, so
it works when the responsible agent has no attached session.

## Agent instructions

Each agent's system instructions include a "Your authority" section generated
from the table for its tier: what it may do, which relation it must hold, and
what only the Principal may do. Anything not listed is outside its authority.

## Migration

The change is additive. No missions, supervisors, or rosters are rewritten, so
existing chief-led missions keep their chief and orchestrator. Authority moves
only through the existing explicit operations (`mission-reassign`,
`agent-reparent`, `mission-staff`).

## Known limits

- The loopback daemon is unauthenticated by design, so the Principal is
  identified by the absence of an agent session. An agent that deliberately
  clears `AO_SESSION_ID` or calls the HTTP API directly would be treated as the
  Principal. The rules stop confused agents taking the wrong step; they are not
  a security boundary against a hostile local process.
- The Principal has no team inbox yet. Mail addressed to the Principal belongs
  to the mailbox v2 work.

## Extending the model

Add an action to `RULES` with a one-line summary and its grants, call
`authorize(store, db, actor, "<action>", **records)` in the operation, and
record the result with `store.record(db, decision, kind, payload)`. Add a
relation to `RELATIONS` when a rule needs a new "the X's Y" grant. Then add the
row to this page. Planned work that builds on this includes mission revisions
and closure approved by the Principal, mailbox v2 relays, job watches, and
owns/may/never role charters.
