"""Use llmwiki's canonical storage and commit only attributable memory edits."""
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import subprocess
import uuid

from .config import ContractError, text


def git(root, *arguments, check=True):
    result = subprocess.run(["git", "-C", str(root), *arguments], capture_output=True, text=True, encoding="utf-8", check=False)
    if check and result.returncode:
        raise ContractError(result.stderr.strip() or "Git command failed")
    return result


def dirty(root, relative):
    return bool(git(root, "status", "--porcelain", "--", relative).stdout)


@contextmanager
def commit_lock(root):
    # Kernel ownership releases after a crash. All team memory operations share it.
    path = root / ".cache" / "ao-team-memory.lock"
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a+b") as stream:
        stream.seek(0)
        if not stream.read(1):
            stream.write(b"0")
            stream.flush()
        stream.seek(0)
        if os.name == "nt":
            import msvcrt
            msvcrt.locking(stream.fileno(), msvcrt.LK_LOCK, 1)
        else:
            import fcntl
            fcntl.flock(stream.fileno(), fcntl.LOCK_EX)
        try:
            yield
        finally:
            stream.seek(0)
            if os.name == "nt":
                msvcrt.locking(stream.fileno(), msvcrt.LK_UNLCK, 1)
            else:
                fcntl.flock(stream.fileno(), fcntl.LOCK_UN)


def file_hash(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def canonical_files(wiki, root):
    return {path.relative_to(root).as_posix(): file_hash(path) for directory in ("wiki", "sources", "events") for path in wiki.path(directory).rglob("*") if path.is_file() and not any(part.startswith(".llmwiki-") for part in path.parts)}


def track_operation(team, wiki, root, intent):
    before = json.loads(intent["before_json"])
    result = json.loads(intent["result_json"]) if intent["result_json"] else None
    events = []
    for path in wiki.path("events").rglob("*.json"):
        relative = path.relative_to(root).as_posix()
        if relative in before["files"]:
            continue
        event = json.loads(path.read_text(encoding="utf-8"))
        if event.get("details", {}).get("team_operation") == intent["id"]:
            events.append(path)
            if result is None:
                result = {"path": event["details"]["path"], "revision": event["details"]["revision"]}
        elif result and intent["operation"] == "memory-capture" and event.get("operation") == "capture" and event.get("subject") == result["metadata"]["id"]:
            events.append(path)
    if result is None:
        changed = [relative for relative, value in canonical_files(wiki, root).items() if before["files"].get(relative) != value]
        if any(dirty(root, relative) for relative in changed):
            raise ContractError("Interrupted memory operation " + intent["id"] + " needs review. Preserve unrelated edits, commit the operation's changes manually, then run memory-recover")
        paths = []
    else:
        page = wiki.path(result["path"])
        if intent["operation"] == "memory-write":
            paths = [page, *events]
            if file_hash(page) != result["revision"] and dirty(root, page.relative_to(root).as_posix()):
                raise ContractError("Memory changed after the operation. Reconcile and commit its edits manually, then run memory-recover")
        else:
            paths = [page, page.with_name("record.json"), *events]
            if file_hash(page) != result["metadata"]["sha256"]:
                raise ContractError("Captured evidence changed unexpectedly")
    changed = []
    for path in paths:
        relative = path.relative_to(root).as_posix()
        content_hash = file_hash(path)
        if before["files"].get(relative) != content_hash:
            changed.append((relative, content_hash, int(relative in before["dirty"])))
    with team.store.connect(True) as db:
        for relative, content_hash, preexisting in changed:
            owner = db.execute("SELECT actor FROM memory_edits WHERE path=? AND actor!=?", (relative, intent["actor"])).fetchone()
            if owner:
                preexisting = 1
            db.execute("INSERT INTO memory_edits VALUES (?,?,?,?) ON CONFLICT(actor,path) DO UPDATE SET content_hash=excluded.content_hash,baseline_dirty=memory_edits.baseline_dirty", (intent["actor"], relative, content_hash, preexisting))
        db.execute("UPDATE memory_operations SET complete=1 WHERE id=?", (intent["id"],))
        team.store.event(db, intent["actor"], "memory.edited", {"paths": [item[0] for item in changed], "operation": intent["id"]})


def recover_operations(team, wiki, root, actor):
    with team.store.connect() as db:
        intents = [dict(row) for row in db.execute("SELECT * FROM memory_operations WHERE actor=? AND complete=0", (actor,))]
    for intent in intents:
        track_operation(team, wiki, root, intent)


def execute(team, actor, operation, arguments):
    workspace = team.request.get("workspace_path")
    wiki = team.wiki()
    if workspace:
        workspace = Path(workspace).resolve()
        source_binding = team.project / ".llmwiki.json"
        target_binding = workspace / ".llmwiki.json"
        expected = json.loads(source_binding.read_text(encoding="utf-8"))
        if target_binding.exists():
            actual = json.loads(target_binding.read_text(encoding="utf-8"))
            if actual.get("project_id") != expected.get("project_id") or actual.get("schema") != expected.get("schema"):
                raise ContractError("Worker memory binding differs from the project namespace")
        else:
            # Exclusive create preserves a binding created concurrently by another tool.
            try:
                with target_binding.open("x", encoding="utf-8") as stream:
                    json.dump(expected, stream, indent=2)
                    stream.write("\n")
            except FileExistsError:
                return execute(team, actor, operation, arguments)
        wiki = team.wiki(workspace)
    root = wiki.memory_root or wiki.project
    if operation == "memory-policy":
        return {"policy": wiki.path("POLICY.md").read_text(encoding="utf-8"), "project_id": wiki.project_id, "store": str(wiki.root)}
    read_operations = {"memory-search": wiki.search, "memory-read": wiki.read, "memory-context": wiki.context, "memory-related": wiki.related, "memory-lint": wiki.lint}
    if operation in read_operations:
        return read_operations[operation](**arguments)
    if operation == "memory-commit":
        message = text(arguments.get("message"), "commit message", 200)
        with commit_lock(root):
            recover_operations(team, wiki, root, actor)
            edits = team.memory_pending(actor)
            pending = []
            for edit in edits:
                path = (root / edit["path"]).resolve()
                if not path.is_relative_to(root.resolve()):
                    raise ContractError("Memory path escaped the configured repository")
                if not dirty(root, edit["path"]):
                    continue
                if edit["baseline_dirty"] or not path.is_file() or file_hash(path) != edit["content_hash"]:
                    raise ContractError("Memory file contains unrelated changes. Reconcile ownership before committing " + edit["path"])
                pending.append(edit["path"])
            if pending:
                # --only leaves unrelated staged and unstaged edits out of this commit.
                git(root, "add", "--", *pending)
                git(root, "commit", "--only", "-m", message, "--", *pending)
            revision = git(root, "rev-parse", "HEAD").stdout.strip()
            with team.store.connect(True) as db:
                db.execute("DELETE FROM memory_edits WHERE actor=?", (actor,))
                team.store.event(db, actor, "memory.committed", {"revision": revision, "paths": pending})
            return {"commit": revision, "paths": pending}
    if operation == "memory-recover":
        with commit_lock(root):
            recover_operations(team, wiki, root, actor)
        return {"pending": team.memory_pending(actor)}
    if operation not in {"memory-write", "memory-capture"}:
        raise ContractError("Unknown memory operation")
    with commit_lock(root):
        if git(root, "rev-parse", "--show-toplevel", check=False).returncode:
            raise ContractError("Initialize datashard as a Git repository before writing team memory")
        recover_operations(team, wiki, root, actor)
        before = canonical_files(wiki, root)
        baseline_dirty = {path for path in before if dirty(root, path)}
        operation_id = uuid.uuid4().hex
        intent = {"id": operation_id, "actor": actor, "operation": operation, "before_json": json.dumps({"files": before, "dirty": sorted(baseline_dirty)}), "result_json": None}
        with team.store.connect(True) as db:
            db.execute("INSERT INTO memory_operations(id,actor,operation,before_json) VALUES (?,?,?,?)", (operation_id, actor, operation, intent["before_json"]))
        if operation == "memory-write":
            result = wiki.write(**arguments, _details={"team_agent": actor, "team_operation": operation_id})
        else:
            result = wiki.capture_source(**arguments)
        intent["result_json"] = json.dumps(result)
        with team.store.connect(True) as db:
            db.execute("UPDATE memory_operations SET result_json=? WHERE id=?", (intent["result_json"], operation_id))
        track_operation(team, wiki, root, intent)
        return result
