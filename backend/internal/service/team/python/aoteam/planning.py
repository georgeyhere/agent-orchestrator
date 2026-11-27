"""Principal-approved planning and explicit project organization changes."""
import json

from .authority import authorize
from .config import ContractError, identifier, text


def revision_number(revision):
    if type(revision) is not int or revision < 1:
        raise ContractError("revision must be a positive integer")


class Planning:
    def __init__(self, store, workflow):
        self.store, self.workflow = store, workflow

    def planner(self, db, actor):
        decision = authorize(self.store, db, actor, "draft.create")
        row = db.execute("SELECT id FROM agents WHERE tier='planner'").fetchone()
        if not row:
            raise ContractError("Create this project's planner first")
        return decision, row[0]

    def create(self, actor, draft, objective, criteria="", plan="", chief=None, supervisor=None, workers=None):
        identifier(draft)
        text(objective, "objective")
        if not isinstance(criteria, str) or not isinstance(plan, str) or len(criteria) > 50000 or len(plan) > 50000:
            raise ContractError("Criteria and plan must be text up to 50000 characters")
        s = self.store
        with s.connect(True) as db:
            decision, planner = self.planner(db, actor)
            supervisor = s.resolve_supervisor(db, chief, supervisor)
            workers = s.validate_workers(db, workers if workers is not None else [], supervisor)
            db.execute("INSERT INTO drafts(id,planner,chief,supervisor,objective,criteria,plan) VALUES (?,?,?,?,?,?,?)",
                       (draft, planner, supervisor, supervisor, objective, criteria, plan))
            s.set_workers(db, "draft", draft, workers)
            s.record(db, decision, "draft.created", {"id": draft})
            s._send(db, actor, planner, f"Plan mission {draft}", "Work with the Principal on this draft. Ask questions in your conversation and save the brief and plan with draft-update. Execution requires the Principal's release.\n" + json.dumps(s.get(db, "drafts", draft)))
            return s.get(db, "drafts", draft)

    def update(self, actor, draft, revision, objective, criteria, plan, chief=None, supervisor=None, workers=None):
        revision_number(revision)
        text(objective, "objective")
        if not isinstance(criteria, str) or not isinstance(plan, str) or len(criteria) > 50000 or len(plan) > 50000:
            raise ContractError("Criteria and plan must be text up to 50000 characters")
        s = self.store
        with s.connect(True) as db:
            item = s.get(db, "drafts", draft)
            decision = authorize(s, db, actor, "draft.update", draft=item)
            if item["status"] != "draft" or item["revision"] != revision:
                raise ContractError("Draft changed. Read its latest revision")
            supervisor = s.resolve_supervisor(db, chief, supervisor)
            workers = s.validate_workers(db, item["workers"] if workers is None else workers, supervisor)
            db.execute("UPDATE drafts SET objective=?,criteria=?,plan=?,chief=?,supervisor=?,revision=revision+1 WHERE id=?", (objective, criteria, plan, supervisor, supervisor, draft))
            s.set_workers(db, "draft", draft, workers)
            s.event(db, decision.actor, "draft.updated", decision.audit({"id": draft, "revision": revision + 1}))
            if decision.principal:
                s._send(db, actor, item["planner"], f"Draft {draft} updated", json.dumps(s.get(db, "drafts", draft)))
            return s.get(db, "drafts", draft)

    def release(self, actor, draft, revision):
        revision_number(revision)
        s = self.store
        with s.connect(True) as db:
            decision = authorize(s, db, actor, "draft.release")
            item = s.get(db, "drafts", draft)
            if item["status"] != "draft" or item["revision"] != revision:
                raise ContractError("Draft changed. Review its latest revision before releasing")
            text(item["criteria"], "acceptance criteria")
            text(item["plan"], "execution plan")
            if not item["supervisor"]:
                raise ContractError("Assign a supervisor before releasing the draft")
            mission = self.workflow.register_mission(db, actor, draft, item["objective"], item["criteria"], supervisor=item["supervisor"], plan=item["plan"], workers=item["workers"], released=True)
            db.execute("UPDATE drafts SET status='released',mission=?,revision=revision+1 WHERE id=?", (draft, draft))
            s.record(db, decision, "draft.released", {"id": draft, "revision": revision + 1})
            s._send(db, actor, item["planner"], f"Draft {draft} released", f"The Principal released this mission to supervisor {item['supervisor']}. Preserve the approved brief; execution now belongs to the mission supervisor and orchestrator.")
            s._send(db, actor, mission["orchestrator"], f"Mission {draft} released", json.dumps(mission))
            return mission

    def reparent(self, actor, agent, parent, revision):
        revision_number(revision)
        s = self.store
        with s.connect(True) as db:
            decision = authorize(s, db, actor, "agent.reparent")
            caller = s.caller(db, decision)
            item, leader = s.get(db, "agents", agent), s.get(db, "agents", parent)
            if item["tier"] != "worker" or leader["tier"] not in {"chief", "orchestrator"}:
                raise ContractError("Move workers between project chiefs and the project orchestrator")
            if caller:
                for supervisor in (item["parent"], parent):
                    row = s.get(db, "agents", supervisor)
                    if supervisor != actor and row["parent"] != actor:
                        raise ContractError("Both supervisors must belong to this orchestrator")
            if item["revision"] != revision:
                raise ContractError("Org chart changed. Read the current worker revision")
            if db.execute("SELECT 1 FROM tasks WHERE worker=? AND status!='accepted'", (agent,)).fetchone() or db.execute("SELECT 1 FROM mission_workers w JOIN missions m ON m.id=w.mission WHERE w.worker=? AND m.status!='approved'", (agent,)).fetchone():
                raise ContractError("Worker has unfinished work. Transfer its mission and workers together or finish the task first")
            if parent != item["parent"]:
                db.execute("UPDATE agents SET parent=?,revision=revision+1 WHERE id=?", (parent, agent))
                s.record(db, decision, "agent.reparented", {"agent": agent, "from": item["parent"], "to": parent})
                for recipient in {agent, parent, item["parent"]}:
                    s._send(db, actor, recipient, f"Worker {agent} reassigned", f"{agent} now reports to {parent}. Read the current org chart with ao team status.")
            return s.get(db, "agents", agent)

    def reassign(self, actor, mission, revision, chief=None, supervisor=None, move_workers=False):
        revision_number(revision)
        if type(move_workers) is not bool:
            raise ContractError("move_workers must be boolean")
        s = self.store
        with s.connect(True) as db:
            item = s.get(db, "missions", mission)
            decision = authorize(s, db, actor, "mission.reassign", mission=item)
            supervisor = s.resolve_supervisor(db, chief, supervisor, require=True)
            leader = s.get(db, "agents", supervisor)
            if leader["tier"] == "chief":
                if leader["parent"] != item["orchestrator"]:
                    raise ContractError("Assign the mission to a chief under its orchestrator")
            elif leader["id"] != item["orchestrator"]:
                raise ContractError("Assign the mission to its orchestrator")
            if item["status"] not in {"active", "blocked"} or item["revision"] != revision:
                raise ContractError("Only current active or blocked missions can be reassigned")
            if supervisor == item["supervisor"]:
                return item
            workers = set(item["workers"])
            if workers and not move_workers:
                raise ContractError("This mission has staffed workers. Explicitly move its roster with it")
            for worker in workers:
                if db.execute("SELECT 1 FROM mission_workers w JOIN missions m ON m.id=w.mission WHERE w.worker=? AND m.id!=? AND m.status!='approved'", (worker, mission)).fetchone():
                    raise ContractError("Worker is staffed on another open mission; revise that staffing before transferring")
                db.execute("UPDATE agents SET parent=?,revision=revision+1 WHERE id=?", (supervisor, worker))
                s._send(db, actor, worker, f"Mission {mission} transferred", f"Your current task remains assigned. Your supervisor is now {supervisor}. Read current revisions before submitting.")
            db.execute("UPDATE tasks SET revision=revision+1 WHERE mission=? AND status!='accepted'", (mission,))
            db.execute("UPDATE missions SET chief=?,supervisor=?,revision=revision+1 WHERE id=?", (supervisor, supervisor, mission))
            s.event(db, decision.actor, "mission.reassigned", {"mission": mission, "from": item["supervisor"], "to": supervisor, "workers": sorted(workers)} | decision.audit())
            for recipient in {item["supervisor"], supervisor, item["orchestrator"]}:
                s._send(db, actor, recipient, f"Mission {mission} reassigned", json.dumps(s.get(db, "missions", mission)))
            return s.get(db, "missions", mission)

    def staff(self, actor, mission, revision, workers):
        revision_number(revision)
        s = self.store
        with s.connect(True) as db:
            item = s.get(db, "missions", mission)
            decision = authorize(s, db, actor, "mission.staff", mission=item)
            if item["status"] not in {"active", "blocked"} or item["revision"] != revision:
                raise ContractError("Mission changed. Staffing can be revised on current active or blocked missions")
            workers = s.validate_workers(db, workers, item["supervisor"], required=True)
            unfinished = {row[0] for row in db.execute("SELECT worker FROM tasks WHERE mission=? AND status!='accepted'", (mission,))}
            if unfinished - set(workers):
                raise ContractError("Finish a worker's tasks before removing it from mission staffing")
            s.set_workers(db, "mission", mission, workers)
            db.execute("UPDATE missions SET revision=revision+1 WHERE id=?", (mission,))
            s.event(db, decision.actor, "mission.staffed", decision.audit({"mission": mission, "workers": workers, "revision": revision + 1}))
            for recipient in set(item["workers"]) | set(workers) | {item["supervisor"]}:
                s._send(db, actor, recipient, f"Mission {mission} staffing updated", json.dumps(s.get(db, "missions", mission)))
            return s.get(db, "missions", mission)
