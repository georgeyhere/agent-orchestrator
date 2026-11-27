# Mission brief and work order format

Strong agents plan and cheap agents execute, so a worker can only use the
judgment the planner wrote down. Package
`backend/internal/service/team/spec` defines the two typed documents that carry
it, the lint that gates release, and the markdown a worker receives.

- **Mission brief** (`spec.Brief`): `goal`, `non_goals`, `context`,
  `decisions`, `interfaces`, `constraints`, `risks`, `acceptance`, `tasks` and
  `staffing`. Every decision a worker could get wrong is either made in
  `decisions` or named in `risks` with an escalation trigger.
- **Work order** (`spec.WorkOrder`): one worker's complete instructions:
  `id`, `title`, `outcome`, `read_first`, `owns`, `steps`, `interfaces`,
  `examples`, `do_not`, `checks`, `evidence`, `escalate_if`, `budget`,
  `depends_on`, `model_tier` and `decisions`.

`testdata/token-auth.yaml` is a complete worked example. Briefs decode from JSON
or YAML and unknown fields are rejected.

## Lint

`spec.Lint(brief, spec.LintOptions{Files: baseCommitTree})` returns findings
that each name their field, such as `tasks[token-parser].steps[2]`. Errors block
release; warnings do not. It checks:

- required fields are present, and run checks have a command that parses and a
  typed `expect`;
- `read_first` paths exist at the base commit, and each `owns` entry's
  directory exists (a task may own a file it creates);
- `depends_on` names known tasks and has no cycles;
- tasks that can run at the same time (no dependency path between them) have no
  overlapping `owns` globs;
- an interface shared by two tasks is byte-identical in both and in the brief;
- titles, outcomes, steps and escalation triggers contain no unresolved wording
  such as "TBD", "figure out", "as appropriate" or "etc.".

Free-text tasks from before work orders wrap as `spec.LegacyWorkOrder` and lint
with a single warning.

## Rendering

`spec.RenderWorkOrder(brief, taskID)` renders a worker's prompt: the order plus
the mission's goal, non-goals, the decisions it names and the outcomes of its
prerequisites. The section order is fixed and it holds no revision numbers, so
it can be used verbatim as a wake prompt. `spec.RenderBrief` renders the whole
brief for planners, reviewers and the GUI.

## Not yet wired

The package is pure and the team runtime does not call it yet. Storing briefs
as typed columns, `task-plan`, `ao team run` checks, the plan-back on
`task-start` and rework causes land with the team runtime phases that own them.
