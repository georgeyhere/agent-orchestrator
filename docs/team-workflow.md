# Project teams

This fork adds a Python workflow service for project orchestrators, mission chiefs,
specialized workers, persistent mail, and llmwiki/datashard knowledge. AO owns
native Codex processes, conversations, worktrees, and agent recovery. The existing
daemon API serves the new Project teams page in the desktop and browser GUI.

Chiefs use AO's native worker runtime. Their team tier defines their authority in
the workflow. Each project team has one orchestrator, optionally one planner, and
any number of chiefs and workers. Custom roles have a fixed tier and their own
standing instructions.
Team providers are Codex (Terminal UI and Chat) and Muse Code (Terminal UI).
Both receive the team mailbox hooks and llmwiki/datashard instructions. On Windows,
Muse is discovered on PATH or at `%LOCALAPPDATA%\Programs\muse\muse.cmd`.

## Run the desktop app

Use Python 3.11+, Git, Node 24+, Go 1.27.4+, an authenticated Codex CLI, and llmwiki
installed in the selected Python environment. An adjacent `llmwiki/src` checkout
can also supply the engine through `AO_LLMWIKI_SOURCE`.

Install dependencies from the repository root.

```sh
npm ci
npm ci --prefix packages/product-ui
npm ci --prefix frontend
python scripts/team-desktop.py --build
```

The first build uses AO's standard Electron Forge package pipeline, including its
native preload bridge, folder picker, bundled Go daemon, and runtime resources.
It can take several minutes. Subsequent launches open the compiled app directly,
without rebuilding Go or starting Vite.

```sh
python scripts/team-desktop.py
```

On Windows, add `--shortcut` once to create an **Agent Teams** desktop shortcut.
The shortcut uses the selected environment's `pythonw.exe`, so no PowerShell window
is needed. It keeps its data directory and memory settings in
`desktop-launch.json` under the chosen state directory. The launcher configures
Python and an adjacent llmwiki source checkout for the native daemon; Python and
llmwiki remain explicit runtime dependencies.

The team launcher opens **Project teams** directly. It provides **Planning**,
**Missions**, **Workers**, **Agents**, **Org chart**, and **Mailbox** views.
Workers show their supervisors, current task and mission, and session controls.
The org chart shows project-level planner/orchestrator, chiefs and their missions,
and the workers reporting to each chief.

Desktop and browser launchers default to the same `~/.ao/team-dev` project state.
Close the browser launcher with Ctrl+C before switching to the desktop app, so
Electron can start and supervise its bundled daemon. Use `--data-dir`, `--port`,
and `--memory-root` to select other state. Quit the app through its normal desktop
quit command; Electron owns daemon shutdown. `--build-only` builds without launch.
Local packages use this fork as their update source via `AO_RELEASE_REPO`.

## Browser development mode

`python scripts/team-dev.py` now opens the desktop app for compatibility with
existing launch commands. Use `python scripts/team-dev.py --browser` for browser development. Native
desktop actions such as the folder picker use Electron's preload bridge, so use
the desktop app for native folder browsing. The team page also accepts a typed
repository path, including in browser mode.

The browser launcher builds AO and starts the daemon and Vite on loopback. Use `--data-dir`,
`--port`, `--gui-port`, `--memory-root`, `--no-browser`, and `--skip-build` as needed.
Logs are `daemon.log` and `gui.log` under the selected development directory.
The launcher can use portable Windows tools installed under `.cache/toolchains`.

For a packaged desktop or independently launched daemon, set `AO_TEAM_PYTHON` to
the Python executable. Set `AO_LLMWIKI_SOURCE` to llmwiki's `src` directory if the
package is supplied from source. Datashard defaults to the `LLMWIKI_MEMORY_ROOT`
environment variable. On Windows, team requests also read the current user/system
environment setting when it is absent from the daemon's inherited environment.
An explicit project override takes precedence. If no variable or override is set,
llmwiki uses its existing binding or standard `~/.llmwiki-memory` location. A root
must contain the namespace identified by an existing project's binding; choosing
an empty datashard does not move or recreate existing project memory.

For example, to configure this Windows user's existing datashard:

```powershell
[Environment]::SetEnvironmentVariable('LLMWIKI_MEMORY_ROOT', 'C:\path\to\datashard', 'User')
```

## Create a team

1. In Project teams, use Add project. Browse for a folder in Electron or enter
   the repository path, then choose Add project folder. The repository needs an
   initial Git commit. Registration opens team setup without starting agents.
2. Open Project teams and select the project.
3. Enable its team. This validates llmwiki and creates a stable `.llmwiki.json`
   binding if the project needs one. Existing namespaces are preserved.
4. Spawn an orchestrator, then chiefs under it and workers under their chiefs.
   Choose a role and provider: Codex Terminal UI/Chat or Muse Terminal UI.
5. Use Create planner to add a project planner, normally in Chat mode. Open its
   conversation through Work with planner. Request mission planning with a draft
   ID and goal, then refine the brief, acceptance criteria, constraints, and plan
   together. The planner saves revisions in the Planning tab.
6. Review the latest draft, choose a chief, and Release mission. Only the human
   operator can release it. Drafts have no execution tasks or active mission until
   release. The approved plan is copied into the mission and remains available.
   Define mission also remains available for direct creation of an active mission.
7. Chiefs assign bounded worker tasks,
   inspect verification evidence, and accept or return worker submissions.
8. A chief submits a mission after all its tasks are accepted. The assigned
   orchestrator approves it or requests changes. Reviews require the current
   revision and feedback. Final prose alone does not change workflow state.

The GUI shows launch errors, pending mail, submission evidence, feedback, and
revision numbers. Pause dispatch suspends launches and idle wakeups. Stop agent
also stops the native agent. Resume retries failed launches or resumes a stopped
native conversation. Open session uses AO's existing conversation or terminal UI.

## Configure the project organization

Mission specs explicitly record `chief` and `workers` (a list of agent IDs).
In Planning, choose the responsible chief and check the named workers under
Mission staffing. A draft can stay incomplete while you discuss the goal, but
release requires a chief and at least one worker reporting to it. The released
mission preserves that roster. Direct mission creation requires the same staffing.

Chiefs can assign execution tasks only to that mission's listed workers. Project
membership alone does not grant task eligibility. A worker can be staffed on
several missions under the same chief, but can have only one unfinished task.
Missions display their staffing even before tasks exist, and the org chart lists
the roster beside each mission. Use Edit staffing on a current active or blocked
mission to change the roster; existing unfinished tasks prevent removing their
worker. Staffing edits require the operator or assigned orchestrator and the
current mission revision. CLI/API: `mission-staff(mission,revision,workers)`.

Transferring a mission moves the entire roster, including workers with no task
yet, when explicitly selected. A worker staffed on another open mission cannot
move until its other staffing is revised. Workers staffed on open missions cannot
be moved individually. Existing task assignments are retained during migration and
used to populate legacy mission rosters. Legacy drafts with no roster require
staffing before release; legacy missions with no tasks require staffing before
new task assignment. No workers are inferred from the org chart.

The Org chart tab is the project's saved organization. It shows the planner and
orchestrator at project level, each chief under its orchestrator, the chief's
missions, and the workers reporting to that chief with their unfinished tasks.
Use Spawn agent to choose custom roles and supervisors when adding team members.
The planner collaborates with you and has no execution authority.

Move worker changes an idle worker's chief with a revision check. A worker with
unfinished work stays with its mission. Transfer mission can move an active or
blocked mission to another chief. When tasks remain unfinished, explicitly select
the option to move those workers too. The transaction changes the chief, worker
supervisors, and unfinished task revisions together, then notifies the affected
agents. The previous chief loses review authority; stale reviews fail. The final
orchestrator remains unchanged. Missions under final review or already approved
cannot be transferred. Accepted tasks and their historical evidence are retained.

Each project has its own planner conversation, drafts, roles, reporting lines,
missions, and mailbox. Switching the project selector selects that project's
organization. Existing team databases upgrade additively on first use.

## Agent and CLI operations

`ao team` resolves its project through `--project`, `AO_PROJECT_ID`, the current
`AO_SESSION_ID`, or the working repository. Inside managed sessions, the daemon
derives the team identity from the native session ID. Cross-project requests and
role-invalid transitions fail. Local AO callers operate within the existing
single-user loopback trust model.

```sh
ao team --project my-project enable
ao team --project my-project status
ao team inbox
ao team act agent-add --file agent.json
ao team act mission-create --file mission.json
ao team act task-assign --file task.json
ao team act submit --file submission.json
ao team act review --file review.json
ao team export
```

Each argument file is one JSON object. These are the operation arguments.

| Operation | Arguments |
| --- | --- |
| `enable` | Optional `memory_root` |
| `role-add` | `role`, `tier`, `instructions` |
| `agent-add` | `agent`, `role`, optional `parent`, `provider` (`codex`), `mode` (`tui` or `chat`) |
| `agent-control` | `agent`, `action` (`pause`, `resume`, `cancel`) |
| `mission-create` | `mission`, `chief`, `objective`, `criteria` |
| `draft-create` | `draft`, `objective`, optional `criteria`, `plan`, `chief` |
| `draft-update` | `draft`, `revision`, `objective`, `criteria`, `plan`, optional `chief` |
| `draft-release` | `draft`, `revision` (human operator only) |
| `agent-reparent` | `agent`, `parent`, `revision` (worker's current org revision) |
| `mission-reassign` | `mission`, `chief`, `revision`, optional boolean `move_workers` |
| `task-assign` | `task`, `mission`, `worker`, `objective` |
| `submit` | `kind` (`task` or `mission`), `record`, `revision`, `result`, `evidence` |
| `review` | `kind`, `record`, `revision`, `approve` (boolean), `feedback` |
| `block` | `kind`, `record`, `revision`, `reason` |
| `unblock` | `kind`, `record`, `revision` |
| `send` | `recipient`, `subject`, `body`, optional stable retry `key` |
| `ack` | `message` |

A role definition might contain the following object.

```json
{"role":"test-engineer","tier":"worker","instructions":"Verify assigned behavior and report reproducible test evidence to the chief."}
```

A submission contains evidence the reviewer can check.

```json
{"kind":"task","record":"verify-parser","revision":1,"result":"Parser handles the requested inputs.","evidence":"Targeted parser tests passed. See the task's recorded command output."}
```

## Durable mail

Python owns `AO_DATA_DIR/teams/<project-id>/team.sqlite3`. Role definitions, native
session mappings, missions, tasks, messages, receipts, operation journals, and
audit events are transactional local records. Mail send retries can use stable
deduplication keys. Workflow changes and their notifications commit together.

Codex SessionStart, UserPromptSubmit, PostToolUse, and Stop hooks surface mail.
Chat launches inspect Codex's hook fingerprints and trust only the four exact
mailbox commands in that thread's config. User hook settings remain untouched.
An explicit recipient acknowledgement records handled or durably delegated mail.
Reading an inbox records delivery. A Stop hook continues a turn while mail or
tracked datashard edits remain pending. The daemon also wakes idle sessions from
durable state, covering mail that arrives after Stop or during daemon downtime.
Paused and unavailable agents retain their pending mail. Failed launches retry
with backoff and pause after three attempts, with the error shown in the GUI.

Mail is delivered at least once. Agents should make side effects idempotent and
acknowledge by message ID. A handled receipt and task acceptance are separate
records. Use `ao team export` for all communication and audit events.

## llmwiki and datashard

Every managed agent receives its role, the team protocol, and its selected
project's memory policy in the system prompt. Restore and lifecycle hooks refresh
those instructions. The Python bridge calls the existing llmwiki storage engine,
preserving namespaces, provenance, revisions, lifecycle states, and evidence.

Use an existing Git-backed datashard when available. For a new memory directory,
enable the team first to create its policy and namespace, then initialize Git,
configure the commit identity, and commit that initial skeleton before assigning
memory-writing work. The memory bridge requires Git and deliberately leaves
repository identity and initial policy review to its owner.

| Operation | Arguments |
| --- | --- |
| `memory-policy` | None |
| `memory-search` | llmwiki search arguments, including `query` and optional `scope` |
| `memory-read` | `record_id`, optional `offset`, `max_chars` |
| `memory-context` | `query`, optional context arguments |
| `memory-related` | `memory_id`, optional `limit` |
| `memory-write` | `record`, optional `memory_id`, `expected_revision` |
| `memory-capture` | `relative_path`, `origin`, `title` |
| `memory-lint` | None |
| `memory-commit` | `message` |
| `memory-recover` | None |

Worker memory operations create or verify the worktree binding against the
original project identity. Source capture uses that worker's working tree.
Search before writing, verify stale knowledge, preserve evidence, and use revision
checks for updates. Durable decisions belong in datashard alongside their sources.

Memory mutations are journaled before crossing into the memory repository.
`memory-commit` stages attributable page, source, and event paths and uses Git's
path-limited commit behavior to preserve unrelated staged edits. Intervening or
preexisting edits require reconciliation. After an interruption, `memory-recover`
reconstructs attributable operations. If attribution is incomplete, review the
memory diff, commit the operation's changes manually, then retry recovery.
Read-only turns need no commit. Push knowledge only when separately authorized.

## Portability and module boundaries

The Go service embeds the Python package and extracts a versioned runtime under
AO's data directory. It invokes an argument array through AO's process helpers,
including hidden windows and child-tree cancellation on Windows. Python uses its
standard library for SQLite, filesystem access, JSON, and platform-specific locks.
Python and llmwiki are explicit runtime prerequisites on each machine.

The Python database is independent of AO's native database. Use the daemon API
for native session changes. Preserve `.llmwiki.json` project IDs when moving or
cloning projects, select the memory repository on the new machine, and reconfigure
machine-specific executable paths. Keep live databases local. Stop the daemon
before copying runtime databases and logs for backup.

## Verification

```sh
python -B -m unittest discover -s backend/internal/service/team/python -p test_team.py
cd backend
go test ./internal/service/team ./internal/httpd/controllers -run 'TestTeam|TestEmbedded'
go test ./internal/adapters/chatdriver/codexappserver -run TestAppServerTeam
```

Run frontend typechecking and the TeamView tests through the existing frontend
scripts. Regenerate OpenAPI and TypeScript API definitions with `npm run api`
after changing the Go contract.
