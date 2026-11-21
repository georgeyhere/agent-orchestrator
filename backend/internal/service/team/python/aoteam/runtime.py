"""One JSON request per invocation, with all durable team state in Python-owned SQLite."""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import sqlite3
import sys
import time
from types import SimpleNamespace

from .config import ContractError, configured_memory_root, identifier, text
from .store import Store, now
from .workflow import Workflow
from .planning import Planning


EXTRA_SCHEMA = """
CREATE TABLE IF NOT EXISTS native_agents (
 agent TEXT PRIMARY KEY REFERENCES agents(id), request_id TEXT NOT NULL UNIQUE,
 session_id TEXT UNIQUE, mode TEXT NOT NULL DEFAULT 'tui', launch_error TEXT,
 attempts INTEGER NOT NULL DEFAULT 0, retry_at REAL NOT NULL DEFAULT 0,
 notified_seq INTEGER NOT NULL DEFAULT 0, notified_at REAL NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS memory_edits (
 actor TEXT NOT NULL, path TEXT NOT NULL, content_hash TEXT NOT NULL, baseline_dirty INTEGER NOT NULL,
 PRIMARY KEY(actor,path));
CREATE TABLE IF NOT EXISTS memory_operations (
 id TEXT PRIMARY KEY, actor TEXT NOT NULL, operation TEXT NOT NULL,
 before_json TEXT NOT NULL, result_json TEXT, complete INTEGER NOT NULL DEFAULT 0);
"""


class Team:
    def __init__(self, request):
        self.request = request
        self.project = Path(request["project_path"]).resolve()
        self.state = Path(request["state_path"]).resolve()
        self.exists = (self.state / "team.sqlite3").exists()
        self.config = SimpleNamespace(project=self.project, state=self.state)
        self.store = None
        self.workflow = None

    def open(self):
        if self.store is None:
            self.store = Store(self.config)
            self.workflow = Workflow(self.store)
            self.planning = Planning(self.store, self.workflow)
            with self.store.connect(True) as db:
                db.executescript(EXTRA_SCHEMA)
        return self.store

    def wiki(self, workspace=None):
        source = os.environ.get("AO_LLMWIKI_SOURCE")
        if source:
            sys.path.insert(0, source)
        try:
            from llmwiki.core import Store as Wiki
        except ImportError as exc:
            raise ContractError("Install llmwiki into AO_TEAM_PYTHON or set AO_LLMWIKI_SOURCE to its src directory") from exc
        self.open()
        with self.store.connect() as db:
            row = db.execute("SELECT value FROM settings WHERE key='memory_root'").fetchone()
        memory_root = row[0] if row and row[0] else configured_memory_root()
        return Wiki(workspace or self.project, memory_root)

    def enable(self, actor, memory_root=None):
        if actor != "operator":
            raise ContractError("Enable a project team from the human control surface")
        self.open()
        if memory_root is not None:
            text(memory_root, "memory root", 4000)
            with self.store.connect(True) as db:
                db.execute("INSERT OR REPLACE INTO settings VALUES ('memory_root',?)", (str(Path(memory_root).expanduser().resolve()),))
        if not (self.project / ".llmwiki.json").exists():
            source = os.environ.get("AO_LLMWIKI_SOURCE")
            if source:
                sys.path.insert(0, source)
            from llmwiki.core import Store as Wiki
            # The explicit enable action authorizes a new stable memory binding.
            project_id = "ao-" + hashlib.sha256(self.request["project_id"].encode()).hexdigest()[:16]
            chosen_root = memory_root or configured_memory_root()
            Wiki.initialize(self.project, project_id=project_id, memory_root=chosen_root)
            # Keep the project binding portable. Machine-specific roots belong
            # to team settings or LLMWIKI_MEMORY_ROOT.
            binding = self.project / ".llmwiki.json"
            value = json.loads(binding.read_text(encoding="utf-8"))
            value.pop("memory_root", None)
            from .config import atomic_json
            atomic_json(binding, value)
        wiki = self.wiki()
        with self.store.connect(True) as db:
            db.execute("INSERT OR REPLACE INTO settings VALUES ('enabled','true')")
            self.store.event(db, actor, "team.enabled", {"memory_project_id": wiki.project_id})
        return self.snapshot()

    def snapshot(self):
        self.open()
        result = self.store.snapshot()
        with self.store.connect() as db:
            native = {row["agent"]: dict(row) for row in db.execute("SELECT * FROM native_agents")}
            enabled = db.execute("SELECT value FROM settings WHERE key='enabled'").fetchone()
            result["events"] = [dict(row) for row in db.execute("SELECT * FROM events ORDER BY seq DESC LIMIT 100")]
        for agent in result["agents"]:
            agent.update(native.get(agent["id"], {}))
        result["enabled"] = bool(enabled and enabled[0] == "true")
        return result

    def actor(self):
        session = self.request.get("session_id")
        if not session:
            return "operator"
        self.open()
        with self.store.connect() as db:
            row = db.execute("SELECT agent FROM native_agents WHERE session_id=?", (session,)).fetchone()
        if not row:
            raise ContractError("This AO session is not attached to a team agent")
        return row[0]

    def attach(self, agent, session_id):
        self.open()
        with self.store.connect(True) as db:
            row = db.execute("SELECT * FROM native_agents WHERE agent=?", (agent,)).fetchone()
            if not row:
                raise ContractError("Unknown team agent")
            if row["session_id"] and row["session_id"] != session_id:
                raise ContractError("Agent already belongs to another native session")
            db.execute("UPDATE native_agents SET session_id=?,launch_error=NULL WHERE agent=?", (session_id, agent))
            cancelled = db.execute("SELECT cancelled FROM agents WHERE id=?", (agent,)).fetchone()[0]
        return {"attached": True, "cancelled": bool(cancelled)}

    def resolve(self, session_id, request_id="", orchestrator=False):
        self.open()
        with self.store.connect() as db:
            row = db.execute("SELECT agent FROM native_agents WHERE session_id=?", (session_id,)).fetchone()
            if row is None and request_id:
                row = db.execute("SELECT agent FROM native_agents WHERE request_id=?", (request_id,)).fetchone()
            if row is None and orchestrator:
                row = db.execute("SELECT n.agent FROM native_agents n JOIN agents a ON a.id=n.agent WHERE a.tier='orchestrator' AND n.session_id IS NULL").fetchone()
        if row is None:
            return {"managed": False}
        self.attach(row[0], session_id)
        return {"managed": True, "agent": row[0]}

    def add_agent(self, actor, agent, role, parent=None, provider="codex", mode="tui"):
        if provider not in {"codex", "muse"}:
            raise ContractError("Agent provider must be codex or muse")
        if provider == "muse" and mode != "tui":
            raise ContractError("Muse team agents use the terminal interface (tui)")
        if mode not in {"tui", "chat"}:
            raise ContractError("Agent interface must be tui or chat")
        self.wiki()  # Validate the exact memory namespace before creating work.
        # Keep registration and the durable launch request in one transaction.
        with self.store.connect() as db:
            definition = self.store.get(db, "roles", role)
            if definition["tier"] == "orchestrator" and db.execute("SELECT 1 FROM agents WHERE tier='orchestrator'").fetchone():
                raise ContractError("Each project team has one orchestrator")
        with self.store.connect(True) as db:
            self.store.register_agent(db, actor, agent, role, parent, provider)
            if definition["tier"] == "orchestrator" and db.execute("SELECT COUNT(*) FROM agents WHERE tier='orchestrator'").fetchone()[0] > 1:
                raise ContractError("Each project team has one orchestrator")
            request_id = "team-" + hashlib.sha256((self.request["project_id"] + "/" + agent).encode()).hexdigest()
            db.execute("INSERT INTO native_agents(agent,request_id,mode) VALUES (?,?,?)", (agent, request_id, mode))
            self.store._send(db, "operator", agent, "Team agent ready", "Read your role and team protocol. Check current missions and tasks, then wait for assignments.")
        return {"id": agent, "launch": "queued"}

    def instructions(self, agent):
        with self.store.connect() as db:
            item = self.store.get(db, "agents", agent)
            role = self.store.get(db, "roles", item["role"])
        wiki = self.wiki()
        policy = wiki.path("POLICY.md").read_text(encoding="utf-8")
        protocol = Path(__file__).with_name("protocol.md").read_text(encoding="utf-8")
        return (f"\n## Project team role\nYour team identity is {agent}, tier {item['tier']}, role {item['role']}, supervisor {item['parent']}.\n"
                + role["instructions"] + "\n" + protocol + "\n## Project memory policy\n" + policy
                + "\nUse `ao team status` and `ao team inbox` to inspect actionable work. "
                "Use `ao team act OPERATION --file arguments.json` for team actions. "
                "The operations are role-add(role,tier,instructions), agent-add(agent,role,parent,provider,mode), "
                "mission-create(mission,supervisor,objective,criteria), task-assign(task,mission,worker,objective), "
                "submit(kind,record,revision,result,evidence), review(kind,record,revision,approve,feedback), "
                "send(recipient,subject,body,key), ack(message), block(kind,record,revision,reason), unblock(kind,record,revision), "
                "Mission specs explicitly name the supervisor (a chief or the project orchestrator) and workers. Tasks can only go to staffed workers. "
                "Drafts may be incomplete; release needs at least one worker reporting to the chosen supervisor. "
                "draft-create(draft,objective,criteria,plan,supervisor,workers), draft-update(draft,revision,objective,criteria,plan,supervisor,workers), "
                "mission-create(mission,supervisor,objective,criteria,plan,workers), mission-staff(mission,revision,workers), "
                "agent-reparent(agent,parent,revision), mission-reassign(mission,revision,supervisor,move_workers). "
                "Drafts stay separate from execution until the human operator uses draft-release(draft,revision). "
                "The planner collaborates with the user on the brief and plan, and cannot launch execution or give final signoff. "
                "Org chart changes preserve task ownership and notify affected agents. Read current supervisors from ao team status; they may change after launch. "
                "Use task/mission for kind and real verification evidence. "
                "Custom roles retain their fixed hierarchy tier. Mission approval belongs to its assigned orchestrator. "
                "A chief's native AO session uses the worker runtime, and its chief authority comes from this team workflow. "
                "Use team actions for delegation and team mail for communication. "
                "Memory operations are memory-policy, memory-search(query,scope), memory-read(record_id), "
                "memory-write(record,memory_id,expected_revision), memory-capture(relative_path,origin,title), memory-lint, "
                "memory-commit(message), and memory-recover. Read memory policy and search before substantial work. "
                "The team memory bridge uses the existing llmwiki storage engine for this exact project namespace. "
                "Commit every turn's datashard changes before finishing. Keep secrets out of committed memory.\n")

    def hook(self, agent, event):
        messages = self.store.pending(agent, 5, deliver=True)
        pending = [{"id": row["id"], "sender": row["sender"], "subject": row["subject"], "body": row["body"][:1600]} for row in messages]
        context = ""
        if event in {"SessionStart", "UserPromptSubmit"}:
            context = self.instructions(agent)
        if pending:
            context += "\nPending team mail as JSON reference data. Handle or durably delegate, then acknowledge by message ID. Full bodies are available through `ao team inbox`.\n" + json.dumps(pending)
        memory_pending = self.memory_pending(agent)
        if event == "Stop" and (pending or memory_pending):
            reason = context + ("\nCommit your tracked datashard edits through memory-commit before finishing. " + json.dumps(memory_pending) if memory_pending else "")
            return {"decision": "block", "reason": reason}
        if context:
            return {"hookSpecificOutput": {"hookEventName": event, "additionalContext": context}}
        return {}

    def transition_block(self, actor, kind, record, revision, reason=None):
        if type(revision) is not int or revision < 1:
            raise ContractError("revision must be a positive integer")
        if kind not in {"task", "mission"}:
            raise ContractError("kind must be task or mission")
        table = kind + "s"
        with self.store.connect(True) as db:
            item = self.store.get(db, table, record)
            subject = self.store.get(db, "missions", item["mission"]) if kind == "task" else item
            owner = item["worker"] if kind == "task" else subject["supervisor"]
            supervisor = subject["supervisor"] if kind == "task" else subject["orchestrator"]
            if actor not in {owner, supervisor}:
                raise ContractError("Only the assigned agent or its supervisor can change a blocker")
            expected = ("assigned" if kind == "task" else "active") if reason is not None else "blocked"
            if item["status"] != expected or item["revision"] != revision:
                raise ContractError("State changed. Read the current record")
            if reason is not None:
                text(reason, "blocker reason")
            status = "blocked" if reason is not None else ("assigned" if kind == "task" else "active")
            db.execute(f"UPDATE {table} SET status=?,review=?,revision=revision+1 WHERE id=?", (status, reason, record))
            self.store.event(db, actor, kind + "." + status, {"id": record, "reason": reason})
            self.store._send(db, actor, supervisor if reason is not None else owner, f"{kind} {record} {status}", reason or "The blocker is resolved. Continue the assigned work.")
            return self.store.get(db, table, record)

    def dispatch(self):
        with self.store.connect() as db:
            agents = [dict(row) for row in db.execute("SELECT a.*,n.request_id,n.session_id,n.mode,n.notified_at,n.notified_seq FROM agents a JOIN native_agents n ON n.agent=a.id WHERE a.enabled=1 AND n.retry_at<=?", (time.time(),))]
        return {"agents": agents}

    def control(self, actor, agent, action):
        if action not in {"pause", "resume", "cancel"}:
            raise ContractError("Agent control supports pause, resume, and cancel")
        result = self.store.control(actor, agent, action)
        if action == "resume":
            with self.store.connect(True) as db:
                db.execute("UPDATE native_agents SET attempts=0,retry_at=0,launch_error=NULL,notified_at=0 WHERE agent=?", (agent,))
        return result

    def launch_result(self, agent, session_id=None, error=None):
        if session_id:
            return self.attach(agent, session_id)
        with self.store.connect(True) as db:
            row = db.execute("SELECT attempts FROM native_agents WHERE agent=?", (agent,)).fetchone()
            attempts = row[0] + 1
            db.execute("UPDATE native_agents SET launch_error=?,attempts=?,retry_at=? WHERE agent=?", (error, attempts, time.time() + min(300, 2 ** min(attempts, 8)), agent))
            if attempts >= 3:
                db.execute("UPDATE agents SET enabled=0,last_error=? WHERE id=?", (error, agent))
            self.store.event(db, "operator", "agent.launch-failed", {"agent": agent, "error": error})
        return {"recorded": True}

    def native_failed(self, agent, error):
        text(error, "native launch error")
        with self.store.connect(True) as db:
            db.execute("UPDATE native_agents SET launch_error=? WHERE agent=?", (error, agent))
            db.execute("UPDATE agents SET enabled=0,last_error=? WHERE id=?", (error, agent))
            self.store.event(db, "operator", "agent.native-failed", {"agent": agent, "error": error})
        return {"recorded": True}

    def wake(self, agent):
        messages = self.store.pending(agent, 1)
        with self.store.connect() as db:
            item = self.store.get(db, "agents", agent)
            if item["tier"] == "planner":
                due = False  # Planning is conversational; do not repeatedly wake an agent waiting on user answers.
            elif item["tier"] == "worker":
                due = bool(db.execute("SELECT 1 FROM tasks WHERE worker=? AND status='assigned'", (agent,)).fetchone())
            elif item["tier"] == "chief":
                due = bool(db.execute("SELECT 1 FROM tasks t JOIN missions m ON m.id=t.mission WHERE m.supervisor=? AND t.status='review'", (agent,)).fetchone())
                due = due or bool(db.execute("SELECT 1 FROM missions m WHERE supervisor=? AND status='active' AND NOT EXISTS (SELECT 1 FROM tasks WHERE mission=m.id AND status!='accepted')", (agent,)).fetchone())
            else:
                due = bool(db.execute("SELECT 1 FROM missions WHERE orchestrator=? AND status='review'", (agent,)).fetchone())
                due = due or bool(db.execute("SELECT 1 FROM tasks t JOIN missions m ON m.id=t.mission WHERE m.supervisor=? AND t.status='review'", (agent,)).fetchone())
                due = due or bool(db.execute("SELECT 1 FROM missions m WHERE supervisor=? AND status='active' AND m.supervisor=m.orchestrator AND NOT EXISTS (SELECT 1 FROM tasks WHERE mission=m.id AND status!='accepted')", (agent,)).fetchone())
        return {"needed": bool(messages or due), "message": "Your team has pending mail or actionable work. Run `ao team inbox` and `ao team status`, handle the work, and acknowledge handled messages. Follow your role and memory protocol."}

    def memory_pending(self, actor):
        with self.store.connect() as db:
            rows = [dict(row) for row in db.execute("SELECT * FROM memory_edits WHERE actor=?", (actor,))]
            unfinished = [{"operation": row[0], "path": "Unfinished memory operation " + row[0]} for row in db.execute("SELECT id FROM memory_operations WHERE actor=? AND complete=0", (actor,))]
        if not rows:
            return unfinished
        from .memory import dirty
        wiki = self.wiki()
        root = wiki.memory_root or wiki.project
        pending = [row for row in rows if dirty(root, row["path"])]
        clean = {row["path"] for row in rows} - {row["path"] for row in pending}
        if clean:
            with self.store.connect(True) as db:
                db.executemany("DELETE FROM memory_edits WHERE actor=? AND path=?", [(actor, path) for path in clean])
        return pending + unfinished

    def memory(self, actor, operation, arguments):
        from .memory import execute
        return execute(self, actor, operation, arguments)

    def execute(self):
        operation = self.request["operation"]
        arguments = self.request.get("arguments") or {}
        if not self.exists and operation not in {"enable", "status"}:
            return {"managed": False} if operation in {"resolve", "instructions", "hook", "dispatch"} else self.disabled()
        if operation == "status" and not self.exists:
            return {"enabled": False, "agents": [], "roles": [], "missions": [], "drafts": [], "tasks": [], "messages": [], "events": [], "pending": {}}
        self.open()
        if operation in {"resolve", "attach", "launch-result", "native-failed", "dispatch", "instructions", "wake", "wake-result"}:
            if operation == "instructions":
                return {"text": self.instructions(arguments["agent"])}
            if operation == "wake-result":
                with self.store.connect(True) as db:
                    db.execute("UPDATE native_agents SET notified_at=? WHERE agent=?", (time.time(), arguments["agent"]))
                return {}
            return {"resolve": self.resolve, "attach": self.attach, "launch-result": self.launch_result, "native-failed": self.native_failed, "dispatch": self.dispatch, "wake": self.wake}[operation](**arguments)
        actor = self.actor()
        if operation == "enable":
            return self.enable(actor, **arguments)
        if operation == "status":
            return self.snapshot()
        if operation == "export":
            return self.store.export()
        if operation == "hook":
            return self.hook(actor, arguments["event"])
        if operation == "inbox":
            agent = arguments.get("agent", actor)
            if actor != "operator" and actor != agent:
                raise ContractError("Read your assigned inbox")
            return self.store.pending(agent, min(100, max(1, int(arguments.get("limit", 20)))), deliver=True)
        if operation.startswith("memory-"):
            return self.memory(actor, operation, arguments)
        methods = {"role-add": self.store.add_role, "agent-add": self.add_agent, "agent-control": self.control,
                   "draft-create": self.planning.create, "draft-update": self.planning.update, "draft-release": self.planning.release,
                   "agent-reparent": self.planning.reparent, "mission-reassign": self.planning.reassign,
                   "mission-staff": self.planning.staff,
                   "mission-create": self.workflow.create_mission, "task-assign": self.workflow.assign_task,
                   "submit": self.workflow.submit, "review": self.workflow.review, "send": self.store.send, "ack": self.store.acknowledge,
                   "block": self.transition_block, "unblock": self.transition_block}
        if operation not in methods:
            raise ContractError("Unknown team operation")
        return methods[operation](actor, **arguments)

    @staticmethod
    def disabled():
        raise ContractError("Enable the project team first")


def main():
    try:
        if sys.version_info < (3, 11):
            raise ContractError("Team orchestration requires Python 3.11 or newer")
        request = json.load(sys.stdin)
        print(json.dumps({"result": Team(request).execute()}, ensure_ascii=False))
    except (ValueError, KeyError, TypeError, OSError, sqlite3.Error, ImportError) as error:
        print(json.dumps({"error": str(error)}, ensure_ascii=False))
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
