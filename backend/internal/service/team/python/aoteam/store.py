"""Transactional team state, immutable communication, and explicit receipts."""
from __future__ import annotations

from contextlib import contextmanager
from datetime import datetime, timezone
import json
import sqlite3
import uuid

from .authority import DEFAULT_PRINCIPAL_NAME, PRINCIPAL, RESERVED_IDS, RULES, authorize
from .config import ContractError, identifier, text


def now():
    return datetime.now(timezone.utc).isoformat()


DEFAULT_ROLES = {
    "planner": ("planner", "Work with the user to clarify mission objectives, scope, constraints, acceptance criteria, and an execution plan. Maintain revisioned mission drafts through draft-create and draft-update. Ask focused questions in your conversation. Only the Principal releases a draft for execution. Do not assign execution tasks or approve missions."),
    "orchestrator": ("orchestrator", "Coordinate project chiefs. Define missions and acceptance criteria. Review evidence and give final mission signoff or request changes. You may also supervise workers directly: assign tasks only to staffed direct reports, review each submission, then record mission results and explicit final signoff."),
    "chief": ("chief", "Drive your mission to completion. Assign bounded tasks to workers, resolve blockers, verify their results, and submit the mission for orchestrator review."),
    "worker": ("worker", "Complete your assigned tasks, verify the result, and report precise evidence to your supervisor: your chief, or your orchestrator on a directly supervised mission. Escalate blockers promptly."),
}


# Supervisor representation: `supervisor` is the canonical direct supervisor of a
# draft or mission and names either a chief or the project orchestrator. The
# supervisor's tier is the supervision mode, so no mode flag can drift. `chief`
# mirrors the supervisor ID so the existing chief-led contract keeps reading
# the same way; new readers use `supervisor`.
SCHEMA = """
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS roles (
 id TEXT PRIMARY KEY, tier TEXT NOT NULL, instructions TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS agents (
 id TEXT PRIMARY KEY, tier TEXT NOT NULL, role TEXT NOT NULL REFERENCES roles(id),
 parent TEXT REFERENCES agents(id), provider TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
 last_error TEXT, cancelled INTEGER NOT NULL DEFAULT 0, revision INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS missions (
 id TEXT PRIMARY KEY, chief TEXT NOT NULL REFERENCES agents(id),
 supervisor TEXT REFERENCES agents(id),
 orchestrator TEXT NOT NULL REFERENCES agents(id), objective TEXT NOT NULL, criteria TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'active', revision INTEGER NOT NULL DEFAULT 1,
 result TEXT, evidence TEXT, review TEXT, plan TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS drafts (
 id TEXT PRIMARY KEY, planner TEXT NOT NULL REFERENCES agents(id), chief TEXT REFERENCES agents(id),
 supervisor TEXT REFERENCES agents(id),
 objective TEXT NOT NULL, criteria TEXT NOT NULL DEFAULT '', plan TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'draft', revision INTEGER NOT NULL DEFAULT 1,
 mission TEXT UNIQUE REFERENCES missions(id));
CREATE TABLE IF NOT EXISTS tasks (
 id TEXT PRIMARY KEY, mission TEXT NOT NULL REFERENCES missions(id), worker TEXT NOT NULL REFERENCES agents(id),
 objective TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'assigned', revision INTEGER NOT NULL DEFAULT 1,
 result TEXT, evidence TEXT, review TEXT);
CREATE TABLE IF NOT EXISTS mission_workers (
 mission TEXT NOT NULL REFERENCES missions(id), worker TEXT NOT NULL REFERENCES agents(id), PRIMARY KEY(mission,worker));
CREATE TABLE IF NOT EXISTS draft_workers (
 draft TEXT NOT NULL REFERENCES drafts(id), worker TEXT NOT NULL REFERENCES agents(id), PRIMARY KEY(draft,worker));
CREATE TABLE IF NOT EXISTS messages (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, sender TEXT NOT NULL,
 recipient TEXT NOT NULL REFERENCES agents(id), subject TEXT NOT NULL, body TEXT NOT NULL,
 created_at TEXT NOT NULL, dedupe_key TEXT, delivered_at TEXT, acknowledged_at TEXT,
 UNIQUE(sender, dedupe_key));
CREATE INDEX IF NOT EXISTS inbox ON messages(recipient, acknowledged_at, seq);
CREATE TABLE IF NOT EXISTS events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT NOT NULL, actor TEXT NOT NULL,
 kind TEXT NOT NULL, payload TEXT NOT NULL);
"""


class Store:
    def __init__(self, config):
        self.config = config
        config.state.mkdir(parents=True, exist_ok=True)
        self.path = config.state / "team.sqlite3"
        with self.connect() as db:
            db.executescript(SCHEMA)
            db.execute("BEGIN IMMEDIATE")
            version = db.execute("SELECT value FROM settings WHERE key='schema'").fetchone()
            if version and version[0] not in {"1", "2", "3", "4"}:
                raise ContractError("Unsupported state schema")
            # Additive upgrade of the Python-owned database preserves live teams.
            for table, column, declaration in (("agents", "revision", "INTEGER NOT NULL DEFAULT 1"),
                                                ("missions", "plan", "TEXT NOT NULL DEFAULT ''"),
                                                ("missions", "supervisor", "TEXT REFERENCES agents(id)"),
                                                ("drafts", "supervisor", "TEXT REFERENCES agents(id)")):
                if column not in {row[1] for row in db.execute(f"PRAGMA table_info({table})")}:
                    db.execute(f"ALTER TABLE {table} ADD COLUMN {column} {declaration}")
            if version and version[0] in {"1", "2"}:
                db.execute("INSERT OR IGNORE INTO mission_workers SELECT DISTINCT mission,worker FROM tasks")
            if version and version[0] in {"1", "2", "3"}:
                db.execute("UPDATE missions SET supervisor=chief WHERE supervisor IS NULL")
                db.execute("UPDATE drafts SET supervisor=chief WHERE supervisor IS NULL")
            db.execute("INSERT OR REPLACE INTO settings VALUES ('schema','4')")
            binding = config.project / ".llmwiki.json"
            if binding.exists():
                identity = json.loads(binding.read_text(encoding="utf-8"))
                project_id = identity.get("project_id") or str(config.project)
                stored = db.execute("SELECT value FROM settings WHERE key='project_id'").fetchone()
                if stored and stored[0] != project_id:
                    raise ContractError("This state directory belongs to another project namespace")
                db.execute("INSERT OR IGNORE INTO settings VALUES ('project_id',?)", (project_id,))
            for role, (tier, instructions) in DEFAULT_ROLES.items():
                db.execute("INSERT OR IGNORE INTO roles VALUES (?,?,?)", (role, tier, instructions))

    @contextmanager
    def connect(self, write=False):
        db = sqlite3.connect(self.path, timeout=30)
        db.row_factory = sqlite3.Row
        db.execute("PRAGMA foreign_keys=ON")
        db.execute("PRAGMA synchronous=FULL")
        try:
            if write:
                db.execute("BEGIN IMMEDIATE")
            yield db
            db.commit()
        except BaseException:
            db.rollback()
            raise
        finally:
            db.close()

    @staticmethod
    def event(db, actor, kind, payload):
        db.execute("INSERT INTO events(at,actor,kind,payload) VALUES (?,?,?,?)",
                   (now(), actor, kind, json.dumps(payload, ensure_ascii=False)))

    @staticmethod
    def get(db, table, key):
        if table not in {"agents", "roles", "missions", "tasks", "messages", "drafts"}:
            raise ContractError("Unknown record type")
        row = db.execute(f"SELECT * FROM {table} WHERE id=?", (key,)).fetchone()
        if row is None:
            raise ContractError(f"Unknown {table} ID: {key}")
        result = dict(row)
        if table in {"missions", "drafts"}:
            owner = "mission" if table == "missions" else "draft"
            result["workers"] = [r[0] for r in db.execute(f"SELECT worker FROM {owner}_workers WHERE {owner}=? ORDER BY worker", (key,))]
        return result

    def resolve_supervisor(self, db, chief, supervisor, require=False):
        """Accept the explicit supervisor or the legacy chief-only shape."""
        if chief is not None and self.get(db, "agents", chief)["tier"] != "chief":
            raise ContractError("chief names a mission chief; name the project orchestrator with supervisor")
        if supervisor is not None and self.get(db, "agents", supervisor)["tier"] not in {"chief", "orchestrator"}:
            raise ContractError("supervisor names a mission chief or the project orchestrator")
        if chief is not None and supervisor is not None and chief != supervisor:
            raise ContractError("chief and supervisor name different supervisors")
        chosen = supervisor if supervisor is not None else chief
        if chosen is None and require:
            raise ContractError("Name a mission chief or the project orchestrator as supervisor")
        return chosen

    def validate_workers(self, db, workers, supervisor, required=False):
        if not isinstance(workers, list) or any(not isinstance(w, str) for w in workers) or len(set(workers)) != len(workers):
            raise ContractError("Workers must be a list of distinct agent IDs")
        if required and not workers:
            raise ContractError("Assign at least one worker in the mission spec")
        for worker in workers:
            target = self.get(db, "agents", worker)
            if target["tier"] != "worker" or not supervisor or target["parent"] != supervisor:
                raise ContractError("Each mission worker must report to the selected supervisor")
        return sorted(workers)

    @staticmethod
    def set_workers(db, kind, owner, workers):
        db.execute(f"DELETE FROM {kind}_workers WHERE {kind}=?", (owner,))
        db.executemany(f"INSERT INTO {kind}_workers VALUES (?,?)", [(owner, worker) for worker in workers])

    @staticmethod
    def caller(db, decision):
        """The acting agent's record, or None when the Principal acted."""
        return None if decision.principal else Store.get(db, "agents", decision.actor)

    def principal(self, db):
        row = db.execute("SELECT value FROM settings WHERE key='principal_name'").fetchone()
        return {"id": PRINCIPAL, "name": row[0] if row else DEFAULT_PRINCIPAL_NAME}

    def set_principal(self, actor, name):
        text(name, "Principal name", 80)
        with self.connect(True) as db:
            decision = authorize(self, db, actor, "principal.update")
            db.execute("INSERT OR REPLACE INTO settings VALUES ('principal_name',?)", (name.strip(),))
            self.record(db, decision, "principal.updated", {"name": name.strip()})
            return self.principal(db)

    def record(self, db, decision, kind, payload, subject=None, notified=()):
        """Audit an authorized action under the actor's own identity.

        When the Principal acted where a rule names a specific agent, that
        agent is told so its view of the record stays current.
        """
        self.event(db, decision.actor, kind, decision.audit(payload))
        if not decision.in_place_of:
            return
        name = self.principal(db)["name"]
        for agent in decision.in_place_of:
            if agent in notified:
                continue
            self._send(db, decision.actor, agent, f"Principal decision: {subject or kind}"[:200],
                       f"The Principal ({name}) acted in your place to {RULES[decision.action].summary}. "
                       "The Principal's decision takes precedence. Read the current record with `ao team status` before acting on it.")

    def add_role(self, actor, role, tier, instructions):
        identifier(role)
        text(instructions, "instructions")
        if tier not in DEFAULT_ROLES:
            raise ContractError("Role tier must be planner, orchestrator, chief or worker")
        with self.connect(True) as db:
            decision = authorize(self, db, actor, "role.create")
            db.execute("INSERT INTO roles VALUES (?,?,?)", (role, tier, instructions))
            self.record(db, decision, "role.created", {"id": role, "tier": tier})
        return {"id": role}

    def add_agent(self, actor, agent, role, parent=None, provider="codex"):
        with self.connect(True) as db:
            return self.register_agent(db, actor, agent, role, parent, provider)

    def register_agent(self, db, actor, agent, role, parent=None, provider="codex"):
        identifier(agent)
        if agent in RESERVED_IDS:
            raise ContractError(f"{agent} is reserved for the Principal and the system")
        if provider not in {"codex", "muse", "claude-code"}:
            raise ContractError("Unknown agent provider")
        decision = authorize(self, db, actor, "agent.create")
        caller = self.caller(db, decision)
        definition = self.get(db, "roles", role)
        tier = definition["tier"]
        if caller and (tier == "orchestrator" or (caller["tier"] == "chief" and (tier != "worker" or parent != actor))):
            raise ContractError("Chiefs may create their workers. Orchestrators may create chiefs and workers")
        if tier in {"orchestrator", "planner"}:
            if parent:
                raise ContractError("Project planners and orchestrators have no parent")
            if db.execute("SELECT 1 FROM agents WHERE tier=?", (tier,)).fetchone():
                raise ContractError(f"Each project team has one {tier}")
        else:
            supervisor = self.get(db, "agents", parent)
            if tier == "chief":
                if supervisor["tier"] != "orchestrator":
                    raise ContractError("chief needs an orchestrator parent")
            elif supervisor["tier"] not in {"chief", "orchestrator"}:
                raise ContractError("worker needs a chief or orchestrator parent")
            if caller and caller["tier"] == "orchestrator" and tier == "worker" and parent != actor and supervisor["parent"] != actor:
                raise ContractError("Worker supervisor must belong to this orchestrator")
            if caller and tier == "chief" and parent != actor:
                raise ContractError("Chief must belong to this orchestrator")
        db.execute("INSERT INTO agents(id,tier,role,parent,provider) VALUES (?,?,?,?,?)", (agent, tier, role, parent, provider))
        self.record(db, decision, "agent.created", {"id": agent, "role": role, "parent": parent, "provider": provider})
        return {"id": agent}

    def control(self, actor, agent, action):
        if action not in {"pause", "resume", "cancel"}:
            raise ContractError("Unknown agent action")
        with self.connect(True) as db:
            target = self.get(db, "agents", agent)
            decision = authorize(self, db, actor, "agent.control", target=target)
            if action == "resume":
                db.execute("UPDATE agents SET enabled=1,cancelled=0,last_error=NULL WHERE id=?", (agent,))
            else:
                db.execute("UPDATE agents SET enabled=0,cancelled=? WHERE id=?", (int(action == "cancel"), agent))
            self.record(db, decision, "agent." + action, {"id": agent}, f"agent {agent} {action}")
        return {"id": agent, "action": action}

    def _send(self, db, sender, recipient, subject, body, key=None):
        text(subject, "subject", 200)
        text(body, "body")
        self.get(db, "agents", recipient)
        if key:
            text(key, "deduplication key", 200)
            previous = db.execute("SELECT * FROM messages WHERE sender=? AND dedupe_key=?", (sender, key)).fetchone()
            if previous:
                if (previous["recipient"], previous["subject"], previous["body"]) != (recipient, subject, body):
                    raise ContractError("Deduplication key already identifies different mail")
                return dict(previous)
        message_id = "msg-" + uuid.uuid4().hex
        db.execute("INSERT INTO messages(id,sender,recipient,subject,body,created_at,dedupe_key) VALUES (?,?,?,?,?,?,?)",
                   (message_id, sender, recipient, subject, body, now(), key))
        self.event(db, sender, "mail.sent", {"id": message_id, "recipient": recipient})
        return self.get(db, "messages", message_id)

    def send(self, actor, recipient, subject, body, key=None):
        with self.connect(True) as db:
            authorize(self, db, actor, "mail.send")
            return self._send(db, actor, recipient, subject, body, key)

    def pending(self, agent, limit=20, deliver=False):
        with self.connect(deliver) as db:
            self.get(db, "agents", agent)
            rows = [dict(row) for row in db.execute(
                "SELECT * FROM messages WHERE recipient=? AND acknowledged_at IS NULL ORDER BY seq LIMIT ?", (agent, limit))]
            if deliver:
                for row in rows:
                    db.execute("UPDATE messages SET delivered_at=COALESCE(delivered_at,?) WHERE id=?", (now(), row["id"]))
            return rows

    def acknowledge(self, actor, message):
        with self.connect(True) as db:
            row = self.get(db, "messages", message)
            authorize(self, db, actor, "mail.ack", message=row)
            if not row["delivered_at"]:
                raise ContractError("Read the inbox before acknowledging mail")
            if not row["acknowledged_at"]:
                db.execute("UPDATE messages SET acknowledged_at=? WHERE id=?", (now(), message))
                self.event(db, actor, "mail.acknowledged", {"id": message})
        return {"id": message, "acknowledged": True}

    def snapshot(self):
        with self.connect() as db:
            result = {table: [dict(row) for row in db.execute(f"SELECT * FROM {table}")] for table in ("roles", "agents", "missions", "tasks", "drafts")}
            for table in ("missions", "drafts"):
                result[table] = [self.get(db, table, item["id"]) for item in result[table]]
            result["messages"] = [dict(row) for row in db.execute("SELECT * FROM messages ORDER BY seq DESC LIMIT 100")]
            result["pending"] = {row[0]: row[1] for row in db.execute("SELECT recipient,COUNT(*) FROM messages WHERE acknowledged_at IS NULL GROUP BY recipient")}
            return result

    def export(self):
        with self.connect() as db:
            return {table: [dict(row) for row in db.execute(f"SELECT * FROM {table} ORDER BY seq")] for table in ("messages", "events")}
