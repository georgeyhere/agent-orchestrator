"""Team workflow regressions using the real llmwiki engine and isolated Git stores."""
import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))
if os.environ.get("AO_LLMWIKI_SOURCE"):
    sys.path.insert(0, os.environ["AO_LLMWIKI_SOURCE"])

from aoteam.config import ContractError
from aoteam.runtime import Team
from aoteam.memory import git


class TeamTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.project = self.root / "project"
        self.project.mkdir()
        self.memory = self.root / "datashard"
        self.request = {"project_id": "project-one", "project_path": str(self.project), "state_path": str(self.root / "state")}
        self.action("enable", {"memory_root": str(self.memory)})
        git(self.memory, "init")
        git(self.memory, "config", "user.name", "Team test")
        git(self.memory, "config", "user.email", "test@example.invalid")
        git(self.memory, "add", ".")
        git(self.memory, "commit", "-m", "Initial memory")
        for agent, role, parent in (("orch", "orchestrator", None), ("chief", "chief", "orch"), ("worker", "worker", "chief")):
            self.action("agent-add", {"agent": agent, "role": role, "parent": parent})
            Team(self.request | {"operation": "attach", "arguments": {"agent": agent, "session_id": "session-" + agent}}).execute()
            self.clear_mail(agent)

    def action(self, operation, arguments=None, actor=None, workspace=None):
        request = self.request | {"operation": operation, "arguments": arguments or {}}
        if actor:
            request["session_id"] = "session-" + actor
        if workspace:
            request["workspace_path"] = str(workspace)
        return Team(request).execute()

    def clear_mail(self, actor):
        for message in self.action("inbox", actor=actor):
            self.action("ack", {"message": message["id"]}, actor)

    def mission(self):
        self.action("mission-create", {"mission": "mission", "chief": "chief", "objective": "Build feature", "criteria": "Tests pass"}, "orch")
        self.action("task-assign", {"task": "task", "mission": "mission", "worker": "worker", "objective": "Implement feature"}, "chief")

    def test_full_mission_requires_chief_review_and_orchestrator_signoff(self):
        self.mission()
        with self.assertRaises(ContractError):
            self.action("submit", {"kind": "mission", "record": "mission", "revision": 1, "result": "Done", "evidence": "Checks"}, "chief")
        self.action("submit", {"kind": "task", "record": "task", "revision": 1, "result": "Done", "evidence": "Unit test passed"}, "worker")
        self.action("review", {"kind": "task", "record": "task", "revision": 2, "approve": True, "feedback": "Verified"}, "chief")
        self.action("submit", {"kind": "mission", "record": "mission", "revision": 1, "result": "Integrated", "evidence": "Integration test passed"}, "chief")
        with self.assertRaises(ContractError):
            self.action("review", {"kind": "mission", "record": "mission", "revision": 2, "approve": True, "feedback": "Self approve"}, "chief")
        result = self.action("review", {"kind": "mission", "record": "mission", "revision": 2, "approve": True, "feedback": "Final signoff"}, "orch")
        self.assertEqual(result["status"], "approved")

    def test_stale_review_cannot_replace_a_new_submission(self):
        self.mission()
        self.action("submit", {"kind": "task", "record": "task", "revision": 1, "result": "First", "evidence": "Test"}, "worker")
        self.action("review", {"kind": "task", "record": "task", "revision": 2, "approve": False, "feedback": "Fix"}, "chief")
        self.action("submit", {"kind": "task", "record": "task", "revision": 3, "result": "Fixed", "evidence": "New test"}, "worker")
        with self.assertRaises(ContractError):
            self.action("review", {"kind": "task", "record": "task", "revision": 2, "approve": True, "feedback": "Old review"}, "chief")

    def test_custom_roles_cannot_escalate_hierarchy(self):
        self.action("role-add", {"role": "tester", "tier": "worker", "instructions": "Verify regression cases"}, "orch")
        self.action("agent-add", {"agent": "tester-one", "role": "tester", "parent": "chief"}, "chief")
        with self.assertRaises(ContractError):
            self.action("agent-add", {"agent": "second-chief", "role": "chief", "parent": "orch"}, "worker")
        with self.assertRaises(ContractError):
            self.action("agent-add", {"agent": "second-orch", "role": "orchestrator"})

    def test_mail_is_durable_until_recipient_handles_it(self):
        message = self.action("send", {"recipient": "worker", "subject": "Work", "body": "Details", "key": "one"}, "chief")
        output = self.action("hook", {"event": "PostToolUse"}, "worker")
        self.assertIn(message["id"], json.dumps(output))
        self.assertEqual(self.action("hook", {"event": "Stop"}, "worker")["decision"], "block")
        with self.assertRaises(ContractError):
            self.action("ack", {"message": message["id"]}, "chief")
        self.action("ack", {"message": message["id"]}, "worker")
        self.assertEqual(self.action("hook", {"event": "Stop"}, "worker"), {})
        self.assertEqual(len(self.action("export")["messages"]), 4)

    def test_concurrent_sends_are_idempotent(self):
        def send(index):
            return self.action("send", {"recipient": "worker", "subject": "Same", "body": "Same", "key": "retry"}, "chief")["id"]
        with concurrent.futures.ThreadPoolExecutor(max_workers=6) as executor:
            messages = list(executor.map(send, range(12)))
        self.assertEqual(len(set(messages)), 1)

    def test_mail_arriving_after_stop_is_found_by_dispatch(self):
        self.assertEqual(self.action("hook", {"event": "Stop"}, "worker"), {})
        self.action("send", {"recipient": "worker", "subject": "Late", "body": "Late mail"}, "chief")
        output = Team(self.request | {"operation": "wake", "arguments": {"agent": "worker"}}).execute()
        self.assertTrue(output["needed"])

    def test_blocked_worker_remains_assigned_until_unblocked(self):
        self.mission()
        self.action("block", {"kind": "task", "record": "task", "revision": 1, "reason": "Need fixture"}, "worker")
        with self.assertRaises(ContractError):
            self.action("task-assign", {"task": "second", "mission": "mission", "worker": "worker", "objective": "Another"}, "chief")
        result = self.action("unblock", {"kind": "task", "record": "task", "revision": 2}, "chief")
        self.assertEqual(result["status"], "assigned")

    def test_launch_identity_recovers_before_spawn_response(self):
        self.action("agent-add", {"agent": "new-worker", "role": "worker", "parent": "chief"}, "chief")
        state = self.action("status")
        item = next(agent for agent in state["agents"] if agent["id"] == "new-worker")
        request = self.request | {"operation": "resolve", "arguments": {"session_id": "new-session", "request_id": item["request_id"]}}
        result = Team(request).execute()
        self.assertEqual(result["agent"], "new-worker")

    def test_cancelled_launch_remains_cancelled_when_session_attaches(self):
        self.action("agent-add", {"agent": "launching", "role": "worker", "parent": "chief"}, "chief")
        self.action("agent-control", {"agent": "launching", "action": "cancel"}, "chief")
        attached = Team(self.request | {"operation": "launch-result", "arguments": {"agent": "launching", "session_id": "native-launching"}}).execute()
        self.assertTrue(attached["cancelled"])
        dispatch = Team(self.request | {"operation": "dispatch"}).execute()
        self.assertNotIn("launching", [agent["id"] for agent in dispatch["agents"]])

    def test_asynchronous_native_failure_is_visible_and_pauses_dispatch(self):
        Team(self.request | {"operation": "native-failed", "arguments": {"agent": "worker", "error": "Provider failed to start"}}).execute()
        worker = next(agent for agent in self.action("status")["agents"] if agent["id"] == "worker")
        self.assertEqual(worker["enabled"], 0)
        self.assertEqual(worker["launch_error"], "Provider failed to start")
        self.action("agent-control", {"agent": "worker", "action": "resume"}, "chief")
        worker = next(agent for agent in self.action("status")["agents"] if agent["id"] == "worker")
        self.assertEqual(worker["enabled"], 1)
        self.assertIsNone(worker["launch_error"])
        with self.assertRaises(ContractError):
            Team(self.request | {"operation": "attach", "arguments": {"agent": "new-worker", "session_id": "wrong-session"}}).execute()

    def test_memory_bridge_preserves_namespace_in_worktree(self):
        worktree = self.root / "worktree"
        worktree.mkdir()
        policy = self.action("memory-policy", actor="worker", workspace=worktree)
        self.assertTrue(policy["project_id"].startswith("ao-"))
        self.assertEqual(json.loads((worktree / ".llmwiki.json").read_text()), json.loads((self.project / ".llmwiki.json").read_text()))
        self.assertNotIn("memory_root", json.loads((self.project / ".llmwiki.json").read_text()))
        (worktree / ".llmwiki.json").write_text(json.dumps({"schema": 2, "project_id": "wrong"}))
        with self.assertRaises(ContractError):
            self.action("memory-policy", actor="worker", workspace=worktree)

    def write_memory(self):
        return self.action("memory-write", {"record": {"title": "Useful finding", "summary": "A durable observation", "body": "Verified local behavior", "kind": "finding", "scope": "project/team", "status": "draft"}}, "worker")

    def test_memory_commit_excludes_unrelated_staged_edits(self):
        unrelated = self.memory / "unrelated.txt"
        unrelated.write_text("user edit", encoding="utf-8")
        git(self.memory, "add", "unrelated.txt")
        self.write_memory()
        self.assertEqual(self.action("hook", {"event": "Stop"}, "worker")["decision"], "block")
        commit = self.action("memory-commit", {"message": "Record useful finding"}, "worker")
        self.assertTrue(commit["paths"])
        self.assertIn("A  unrelated.txt", git(self.memory, "status", "--porcelain").stdout)
        self.assertNotIn("unrelated.txt", git(self.memory, "show", "--pretty=format:", "--name-only", "HEAD").stdout)
        self.assertEqual(self.action("hook", {"event": "Stop"}, "worker"), {})

    def test_memory_commit_refuses_intervening_changes(self):
        result = self.write_memory()
        wiki = Team(self.request | {"operation": "status"}); wiki.open()
        page = wiki.wiki().path(result["path"])
        page.write_text(page.read_text() + "\nConcurrent user edit\n", encoding="utf-8")
        with self.assertRaises(ContractError):
            self.action("memory-commit", {"message": "Unsafe mixed commit"}, "worker")
        self.assertIn("Concurrent user edit", page.read_text())

    def test_interrupted_memory_tracking_recovers_from_durable_operation(self):
        with patch("aoteam.memory.track_operation", side_effect=OSError("Simulated interruption")):
            with self.assertRaises(OSError):
                self.write_memory()
        pending = self.action("hook", {"event": "Stop"}, "worker")
        self.assertEqual(pending["decision"], "block")
        committed = self.action("memory-commit", {"message": "Recover interrupted finding"}, "worker")
        self.assertTrue(committed["paths"])
        self.assertEqual(self.action("hook", {"event": "Stop"}, "worker"), {})

    def test_memory_operation_tracks_only_its_own_page_and_event(self):
        import aoteam.memory as module
        original = module.track_operation
        unrelated = self.memory / "projects" / next((self.memory / "projects").iterdir()).name / "wiki" / "unrelated.md"
        def concurrent_user_edit(*args):
            unrelated.parent.mkdir(parents=True, exist_ok=True)
            unrelated.write_text("User's independent work", encoding="utf-8")
            return original(*args)
        with patch("aoteam.memory.track_operation", side_effect=concurrent_user_edit):
            self.write_memory()
        committed = self.action("memory-commit", {"message": "Record only my finding"}, "worker")
        self.assertNotIn(unrelated.relative_to(self.memory).as_posix(), committed["paths"])
        self.assertIn("unrelated.md", git(self.memory, "status", "--porcelain").stdout)


if __name__ == "__main__":
    unittest.main()
