"""Transactional team state, immutable communication, and explicit receipts."""
from __future__ import annotations

from contextlib import contextmanager
from datetime import datetime, timezone
import json
import sqlite3
import uuid

from .config import ContractError, identifier, text


def now():
    return datetime.now(timezone.utc).isoformat()


DEFAULT_ROLES = {
    "orchestrator": ("orchestrator", "Coordinate project chiefs. Define missions and acceptance criteria. Review evidence and give final mission signoff or request changes."),
    "chief": ("chief", "Drive your mission to completion. Assign bounded tasks to workers, resolve blockers, verify their results, and submit the mission for orchestrator review."),
    "worker": ("worker", "Complete your assigned tasks, verify the result, and report precise evidence to your chief. Escalate blockers promptly."),
}


SCHEMA = """
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS roles (
 id TEXT PRIMARY KEY, tier TEXT NOT NULL, instructions TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS agents (
 id TEXT PRIMARY KEY, tier TEXT NOT NULL, role TEXT NOT NULL REFERENCES roles(id),
 parent TEXT REFERENCES agents(id), provider TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
 last_error TEXT, cancelled INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS missions (
 id TEXT PRIMARY KEY, chief TEXT NOT NULL REFERENCES agents(id),
 orchestrator TEXT NOT NULL REFERENCES agents(id), objective TEXT NOT NULL, criteria TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'active', revision INTEGER NOT NULL DEFAULT 1,
 result TEXT, evidence TEXT, review TEXT);
CREATE TABLE IF NOT EXISTS tasks (
 id TEXT PRIMARY KEY, mission TEXT NOT NULL REFERENCES missions(id), worker TEXT NOT NULL REFERENCES agents(id),
 objective TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'assigned', revision INTEGER NOT NULL DEFAULT 1,
 result TEXT, evidence TEXT, review TEXT);
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
            version = db.execute("SELECT value FROM settings WHERE key='schema'").fetchone()
            if version and version[0] != "1":
                raise ContractError("Unsupported state schema")
            db.execute("INSERT OR IGNORE INTO settings VALUES ('schema','1')")
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
        if table not in {"agents", "roles", "missions", "tasks", "messages"}:
            raise ContractError("Unknown record type")
        row = db.execute(f"SELECT * FROM {table} WHERE id=?", (key,)).fetchone()
        if row is None:
            raise ContractError(f"Unknown {table} ID: {key}")
        return dict(row)

    def require(self, db, actor, tiers):
        if actor == "operator":
            return None
        agent = self.get(db, "agents", actor)
        if agent["tier"] not in tiers:
            raise ContractError("This operation requires " + " or ".join(tiers))
        return agent

    def add_role(self, actor, role, tier, instructions):
        identifier(role)
        text(instructions, "instructions")
        if tier not in DEFAULT_ROLES:
            raise ContractError("Role tier must be orchestrator, chief or worker")
        with self.connect(True) as db:
            self.require(db, actor, {"orchestrator"})
            db.execute("INSERT INTO roles VALUES (?,?,?)", (role, tier, instructions))
            self.event(db, actor, "role.created", {"id": role, "tier": tier})
        return {"id": role}

    def add_agent(self, actor, agent, role, parent=None, provider="codex"):
        with self.connect(True) as db:
            return self.register_agent(db, actor, agent, role, parent, provider)

    def register_agent(self, db, actor, agent, role, parent=None, provider="codex"):
        identifier(agent)
        if agent == "operator":
            raise ContractError("operator is reserved for the human control surface")
        if provider not in {"codex", "muse"}:
            raise ContractError("Unknown agent provider")
        caller = self.require(db, actor, {"orchestrator", "chief"})
        definition = self.get(db, "roles", role)
        tier = definition["tier"]
        if caller and (tier == "orchestrator" or (caller["tier"] == "chief" and (tier != "worker" or parent != actor))):
            raise ContractError("Chiefs may create their workers. Orchestrators may create chiefs and workers")
        if tier == "orchestrator":
            if parent:
                raise ContractError("An orchestrator has no parent")
        else:
            supervisor = self.get(db, "agents", parent)
            expected = {"chief": "orchestrator", "worker": "chief"}[tier]
            if supervisor["tier"] != expected:
                raise ContractError(f"{tier} needs a {expected} parent")
            if caller and caller["tier"] == "orchestrator" and tier == "worker" and supervisor["parent"] != actor:
                raise ContractError("Worker chief must belong to this orchestrator")
            if caller and tier == "chief" and parent != actor:
                raise ContractError("Chief must belong to this orchestrator")
        db.execute("INSERT INTO agents(id,tier,role,parent,provider) VALUES (?,?,?,?,?)", (agent, tier, role, parent, provider))
        self.event(db, actor, "agent.created", {"id": agent, "role": role, "parent": parent, "provider": provider})
        return {"id": agent}

    def control(self, actor, agent, action):
        if action not in {"pause", "resume", "cancel"}:
            raise ContractError("Unknown agent action")
        with self.connect(True) as db:
            caller = self.require(db, actor, {"orchestrator", "chief"})
            target = self.get(db, "agents", agent)
            if caller and target["parent"] != actor:
                raise ContractError("Agent control requires the direct supervisor")
            if action == "resume":
                db.execute("UPDATE agents SET enabled=1,cancelled=0,last_error=NULL WHERE id=?", (agent,))
            else:
                db.execute("UPDATE agents SET enabled=0,cancelled=? WHERE id=?", (int(action == "cancel"), agent))
            self.event(db, actor, "agent." + action, {"id": agent})
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
            self.require(db, actor, set(DEFAULT_ROLES))
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
            if row["recipient"] != actor:
                raise ContractError("Only the recipient can acknowledge mail")
            if not row["delivered_at"]:
                raise ContractError("Read the inbox before acknowledging mail")
            if not row["acknowledged_at"]:
                db.execute("UPDATE messages SET acknowledged_at=? WHERE id=?", (now(), message))
                self.event(db, actor, "mail.acknowledged", {"id": message})
        return {"id": message, "acknowledged": True}

    def snapshot(self):
        with self.connect() as db:
            result = {table: [dict(row) for row in db.execute(f"SELECT * FROM {table}")] for table in ("roles", "agents", "missions", "tasks")}
            result["messages"] = [dict(row) for row in db.execute("SELECT * FROM messages ORDER BY seq DESC LIMIT 100")]
            result["pending"] = {row[0]: row[1] for row in db.execute("SELECT recipient,COUNT(*) FROM messages WHERE acknowledged_at IS NULL GROUP BY recipient")}
            return result

    def export(self):
        with self.connect() as db:
            return {table: [dict(row) for row in db.execute(f"SELECT * FROM {table} ORDER BY seq")] for table in ("messages", "events")}
