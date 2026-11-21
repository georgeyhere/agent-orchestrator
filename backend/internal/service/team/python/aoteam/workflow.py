"""Mission and task transitions commit together with their mailbox notifications."""
import json

from .config import ContractError, identifier, text


class Workflow:
    def __init__(self, store):
        self.store = store

    def create_mission(self, actor, mission, objective, criteria, chief=None, supervisor=None, plan="", workers=None):
        identifier(mission)
        text(objective, "objective")
        text(criteria, "acceptance criteria")
        if plan:
            text(plan, "execution plan")
        s = self.store
        with s.connect(True) as db:
            return self.register_mission(db, actor, mission, objective, criteria, chief, supervisor, plan, workers)

    def register_mission(self, db, actor, mission, objective, criteria, chief=None, supervisor=None, plan="", workers=None):
        s = self.store
        s.require(db, actor, {"orchestrator"})
        supervisor = s.resolve_supervisor(db, chief, supervisor, require=True)
        leader = s.get(db, "agents", supervisor)
        if leader["tier"] == "chief":
            if actor != "operator" and leader["parent"] != actor:
                raise ContractError("Assign missions to a chief under this orchestrator")
            orchestrator = leader["parent"]
        else:
            if actor != "operator" and leader["id"] != actor:
                raise ContractError("Directly supervised missions belong to the assigning orchestrator")
            orchestrator = leader["id"]
        workers = s.validate_workers(db, workers, supervisor, required=True)
        db.execute("INSERT INTO missions(id,chief,supervisor,orchestrator,objective,criteria,plan) VALUES (?,?,?,?,?,?,?)",
                   (mission, supervisor, supervisor, orchestrator, objective, criteria, plan))
        s.set_workers(db, "mission", mission, workers)
        s.event(db, actor, "mission.created", {"id": mission, "chief": supervisor, "supervisor": supervisor})
        s._send(db, actor, supervisor, f"Mission {mission}", json.dumps(s.get(db, "missions", mission)))
        for worker in workers:
            s._send(db, actor, worker, f"Staffed on mission {mission}", f"Supervisor: {supervisor}. Objective: {objective}. Wait for a task from your supervisor.")
        return s.get(db, "missions", mission)

    def assign_task(self, actor, task, mission, worker, objective):
        identifier(task)
        text(objective, "objective")
        s = self.store
        with s.connect(True) as db:
            item = s.get(db, "missions", mission)
            target = s.get(db, "agents", worker)
            if actor not in {"operator", item["supervisor"]}:
                raise ContractError("Only the mission supervisor can assign its tasks")
            if item["status"] != "active":
                raise ContractError("Tasks can be assigned while a mission is active")
            if target["tier"] != "worker" or target["parent"] != item["supervisor"]:
                raise ContractError("Task worker must belong to the mission supervisor")
            if worker not in item["workers"]:
                raise ContractError("Task worker must be explicitly staffed in the mission spec")
            busy = db.execute("SELECT 1 FROM tasks WHERE worker=? AND status!='accepted'", (worker,)).fetchone()
            if busy:
                raise ContractError("Worker already has an unfinished task")
            db.execute("INSERT INTO tasks(id,mission,worker,objective) VALUES (?,?,?,?)", (task, mission, worker, objective))
            s.event(db, actor, "task.assigned", {"id": task, "worker": worker})
            s._send(db, actor, worker, f"Task {task}", json.dumps(s.get(db, "tasks", task)))
            return s.get(db, "tasks", task)

    def submit(self, actor, kind, record, revision, result, evidence):
        if type(revision) is not int or revision < 1:
            raise ContractError("revision must be a positive integer")
        if kind not in {"task", "mission"}:
            raise ContractError("Unknown submission kind")
        text(result, "result")
        text(evidence, "verification evidence")
        s = self.store
        table = kind + "s"
        with s.connect(True) as db:
            item = s.get(db, table, record)
            expected_actor = item["worker"] if kind == "task" else item["supervisor"]
            allowed_status = "assigned" if kind == "task" else "active"
            if actor != expected_actor:
                raise ContractError("Submission requires the assigned agent")
            if item["status"] != allowed_status or item["revision"] != revision:
                raise ContractError("State changed. Read the latest record before submitting")
            if kind == "mission":
                unfinished = db.execute("SELECT id FROM tasks WHERE mission=? AND status!='accepted'", (record,)).fetchall()
                if unfinished:
                    raise ContractError("Review and accept every task before submitting the mission")
                reviewer = item["orchestrator"]
            else:
                mission = s.get(db, "missions", item["mission"])
                if mission["status"] != "active":
                    raise ContractError("Task mission must be active")
                reviewer = mission["supervisor"]
            db.execute(f"UPDATE {table} SET status='review',revision=revision+1,result=?,evidence=? WHERE id=?", (result, evidence, record))
            s.event(db, actor, kind + ".submitted", {"id": record, "revision": revision + 1})
            s._send(db, actor, reviewer, f"Review {kind} {record}", json.dumps(s.get(db, table, record)))
            return s.get(db, table, record)

    def review(self, actor, kind, record, revision, approve, feedback):
        if type(revision) is not int or revision < 1:
            raise ContractError("revision must be a positive integer")
        if kind not in {"task", "mission"} or type(approve) is not bool:
            raise ContractError("Review needs task/mission and a boolean approve")
        text(feedback, "review feedback")
        table = kind + "s"
        s = self.store
        with s.connect(True) as db:
            item = s.get(db, table, record)
            if kind == "mission":
                reviewer, recipient = item["orchestrator"], item["supervisor"]
                status = "approved" if approve else "active"
            else:
                mission = s.get(db, "missions", item["mission"])
                reviewer, recipient = mission["supervisor"], item["worker"]
                status = "accepted" if approve else "assigned"
            if actor != reviewer:
                raise ContractError(f"Review requires {reviewer}")
            if item["status"] != "review" or item["revision"] != revision:
                raise ContractError("State changed. Read the latest submission before reviewing")
            db.execute(f"UPDATE {table} SET status=?,review=?,revision=revision+1 WHERE id=?", (status, feedback, record))
            s.event(db, actor, kind + ".reviewed", {"id": record, "status": status, "feedback": feedback, "revision": revision + 1})
            s._send(db, actor, recipient, f"{kind.capitalize()} {record} {status}", feedback)
            return s.get(db, table, record)
