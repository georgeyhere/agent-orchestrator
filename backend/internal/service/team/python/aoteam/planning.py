"""Human-approved planning and explicit project organization changes."""
import json

from .config import ContractError, identifier, text


def revision_number(revision):
    if type(revision) is not int or revision < 1:
        raise ContractError("revision must be a positive integer")


class Planning:
    def __init__(self, store, workflow):
        self.store, self.workflow = store, workflow

    def planner(self, db, actor):
        s = self.store
        s.require(db, actor, {"planner", "orchestrator"})
        row = db.execute("SELECT id FROM agents WHERE tier='planner'").fetchone()
        if not row:
            raise ContractError("Create this project's planner first")
        return row[0]

    def chief(self, db, chief):
        if chief and self.store.get(db, "agents", chief)["tier"] != "chief":
            raise ContractError("Select a mission chief")

    def create(self, actor, draft, objective, criteria="", plan="", chief=None):
        identifier(draft)
        text(objective, "objective")
        if not isinstance(criteria, str) or not isinstance(plan, str) or len(criteria) > 50000 or len(plan) > 50000:
            raise ContractError("Criteria and plan must be text up to 50000 characters")
        s = self.store
        with s.connect(True) as db:
            planner = self.planner(db, actor)
            self.chief(db, chief)
            db.execute("INSERT INTO drafts(id,planner,chief,objective,criteria,plan) VALUES (?,?,?,?,?,?)",
                       (draft, planner, chief, objective, criteria, plan))
            s.event(db, actor, "draft.created", {"id": draft})
            s._send(db, actor, planner, f"Plan mission {draft}", "Work with the user on this draft. Ask questions in your conversation and save the brief and plan with draft-update. Execution requires human release.\n" + json.dumps(s.get(db, "drafts", draft)))
            return s.get(db, "drafts", draft)

    def update(self, actor, draft, revision, objective, criteria, plan, chief=None):
        revision_number(revision)
        text(objective, "objective")
        if not isinstance(criteria, str) or not isinstance(plan, str) or len(criteria) > 50000 or len(plan) > 50000:
            raise ContractError("Criteria and plan must be text up to 50000 characters")
        s = self.store
        with s.connect(True) as db:
            item = s.get(db, "drafts", draft)
            if actor not in {"operator", item["planner"]}:
                raise ContractError("Only the user and this project's planner can edit its draft")
            if item["status"] != "draft" or item["revision"] != revision:
                raise ContractError("Draft changed. Read its latest revision")
            self.chief(db, chief)
            db.execute("UPDATE drafts SET objective=?,criteria=?,plan=?,chief=?,revision=revision+1 WHERE id=?", (objective, criteria, plan, chief, draft))
            s.event(db, actor, "draft.updated", {"id": draft, "revision": revision + 1})
            if actor == "operator":
                s._send(db, actor, item["planner"], f"Draft {draft} updated", json.dumps(s.get(db, "drafts", draft)))
            return s.get(db, "drafts", draft)

    def release(self, actor, draft, revision):
        revision_number(revision)
        if actor != "operator":
            raise ContractError("Only the human operator can release a mission draft")
        s = self.store
        with s.connect(True) as db:
            item = s.get(db, "drafts", draft)
            if item["status"] != "draft" or item["revision"] != revision:
                raise ContractError("Draft changed. Review its latest revision before releasing")
            text(item["criteria"], "acceptance criteria")
            text(item["plan"], "execution plan")
            if not item["chief"]:
                raise ContractError("Assign a chief before releasing the draft")
            mission = self.workflow.register_mission(db, actor, draft, item["chief"], item["objective"], item["criteria"], item["plan"])
            db.execute("UPDATE drafts SET status='released',mission=?,revision=revision+1 WHERE id=?", (draft, draft))
            s.event(db, actor, "draft.released", {"id": draft, "revision": revision + 1})
            s._send(db, actor, item["planner"], f"Draft {draft} released", "The user released this mission to its chief. Preserve the approved brief; execution now belongs to the orchestrator and chief.")
            s._send(db, actor, mission["orchestrator"], f"Mission {draft} released", json.dumps(mission))
            return mission

    def reparent(self, actor, agent, parent, revision):
        revision_number(revision)
        s = self.store
        with s.connect(True) as db:
            caller = s.require(db, actor, {"orchestrator"})
            item, leader = s.get(db, "agents", agent), s.get(db, "agents", parent)
            if item["tier"] != "worker" or leader["tier"] != "chief":
                raise ContractError("Move workers between project chiefs")
            old = s.get(db, "agents", item["parent"])
            if caller and (old["parent"] != actor or leader["parent"] != actor):
                raise ContractError("Both chiefs must belong to this orchestrator")
            if item["revision"] != revision:
                raise ContractError("Org chart changed. Read the current worker revision")
            if db.execute("SELECT 1 FROM tasks WHERE worker=? AND status!='accepted'", (agent,)).fetchone():
                raise ContractError("Worker has unfinished work. Transfer its mission and workers together or finish the task first")
            if parent != item["parent"]:
                db.execute("UPDATE agents SET parent=?,revision=revision+1 WHERE id=?", (parent, agent))
                s.event(db, actor, "agent.reparented", {"agent": agent, "from": item["parent"], "to": parent})
                for recipient in {agent, parent, item["parent"]}:
                    s._send(db, actor, recipient, f"Worker {agent} reassigned", f"{agent} now reports to {parent}. Read the current org chart with ao team status.")
            return s.get(db, "agents", agent)

    def reassign(self, actor, mission, chief, revision, move_workers=False):
        revision_number(revision)
        if type(move_workers) is not bool:
            raise ContractError("move_workers must be boolean")
        s = self.store
        with s.connect(True) as db:
            s.require(db, actor, {"orchestrator"})
            item, leader = s.get(db, "missions", mission), s.get(db, "agents", chief)
            if actor not in {"operator", item["orchestrator"]} or leader["tier"] != "chief" or leader["parent"] != item["orchestrator"]:
                raise ContractError("Assign the mission to a chief under its orchestrator")
            if item["status"] not in {"active", "blocked"} or item["revision"] != revision:
                raise ContractError("Only current active or blocked missions can be reassigned")
            if chief == item["chief"]:
                return item
            workers = {row[0] for row in db.execute("SELECT worker FROM tasks WHERE mission=? AND status!='accepted'", (mission,))}
            if workers and not move_workers:
                raise ContractError("This mission has unfinished tasks. Explicitly move its workers with it")
            for worker in workers:
                db.execute("UPDATE agents SET parent=?,revision=revision+1 WHERE id=?", (chief, worker))
                s._send(db, actor, worker, f"Mission {mission} transferred", f"Your current task remains assigned. Your chief is now {chief}. Read current revisions before submitting.")
            db.execute("UPDATE tasks SET revision=revision+1 WHERE mission=? AND status!='accepted'", (mission,))
            db.execute("UPDATE missions SET chief=?,revision=revision+1 WHERE id=?", (chief, mission))
            s.event(db, actor, "mission.reassigned", {"mission": mission, "from": item["chief"], "to": chief, "workers": sorted(workers)})
            for recipient in {item["chief"], chief, item["orchestrator"]}:
                s._send(db, actor, recipient, f"Mission {mission} reassigned", json.dumps(s.get(db, "missions", mission)))
            return s.get(db, "missions", mission)
