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

    def test_enable_uses_environment_without_persisting_machine_path(self):
        project = self.root / "environment-project"
        project.mkdir()
        memory = self.root / "environment-memory"
        request = self.request | {"project_id": "environment-project", "project_path": str(project),
                                  "state_path": str(self.root / "environment-state"), "operation": "enable", "arguments": {}}
        with patch.dict(os.environ, {"LLMWIKI_MEMORY_ROOT": str(memory)}):
            team = Team(request)
            self.assertTrue(team.execute()["enabled"])
            self.assertEqual(team.wiki().memory_root, memory)
            with team.store.connect() as db:
                self.assertIsNone(db.execute("SELECT value FROM settings WHERE key='memory_root'").fetchone())
        self.assertNotIn("memory_root", json.loads((project / ".llmwiki.json").read_text()))

    def test_explicit_memory_root_overrides_environment(self):
        with patch.dict(os.environ, {"LLMWIKI_MEMORY_ROOT": str(self.root / "other-memory")}):
            team = Team(self.request)
            self.assertEqual(team.wiki().memory_root, self.memory)

    def planner(self):
        self.action("agent-add", {"agent": "planner", "role": "planner", "mode": "chat"})
        Team(self.request | {"operation": "attach", "arguments": {"agent": "planner", "session_id": "session-planner"}}).execute()
        self.clear_mail("planner")

    def test_muse_workers_use_the_same_mailbox_memory_and_lifecycle_contract(self):
        self.action("agent-add", {"agent": "muse-worker", "role": "worker", "parent": "chief", "provider": "muse", "mode": "tui"})
        Team(self.request | {"operation": "attach", "arguments": {"agent": "muse-worker", "session_id": "session-muse-worker"}}).execute()
        self.assertEqual(self.action("hook", {"event": "Stop"}, "muse-worker")["decision"], "block")
        self.clear_mail("muse-worker")
        self.assertEqual(self.action("hook", {"event": "Stop"}, "muse-worker"), {})
        self.assertEqual(self.action("memory-policy", actor="muse-worker")["project_id"], Team(self.request).wiki().project_id)
        with self.assertRaisesRegex(ContractError, "terminal interface"):
            self.action("agent-add", {"agent": "invalid-muse", "role": "worker", "parent": "chief", "provider": "muse", "mode": "chat"})

    def test_claude_code_workers_use_the_same_contract_in_terminal_and_chat(self):
        for mode in ("tui", "chat"):
            agent = "claude-worker-" + mode
            self.action("agent-add", {"agent": agent, "role": "worker", "parent": "chief", "provider": "claude-code", "mode": mode})
            Team(self.request | {"operation": "attach", "arguments": {"agent": agent, "session_id": "session-" + agent}}).execute()
            self.assertEqual(self.action("hook", {"event": "Stop"}, agent)["decision"], "block")
            self.clear_mail(agent)
            self.assertEqual(self.action("hook", {"event": "Stop"}, agent), {})
            self.assertEqual(self.action("memory-policy", actor=agent)["project_id"], Team(self.request).wiki().project_id)
        with self.assertRaisesRegex(ContractError, "must be codex"):
            self.action("agent-add", {"agent": "invalid-provider", "role": "worker", "parent": "chief", "provider": "cursor", "mode": "tui"})

    def second_chief(self):
        self.action("agent-add", {"agent": "chief-two", "role": "chief", "parent": "orch"}, "orch")
        Team(self.request | {"operation": "attach", "arguments": {"agent": "chief-two", "session_id": "session-chief-two"}}).execute()
        self.clear_mail("chief-two")

    def draft(self):
        self.planner()
        return self.action("draft-create", {"draft": "planned-feature", "objective": "Export reports", "criteria": "Filters preserved and tests pass", "plan": "Inspect report API, implement export, verify behavior", "chief": "chief", "workers": ["worker"]}, "planner")

    def test_planner_drafts_do_not_dispatch_execution_until_human_release(self):
        self.draft()
        self.assertEqual(self.action("status")["missions"], [])
        self.assertEqual(self.action("inbox", actor="chief"), [])
        with self.assertRaises(ContractError):
            self.action("draft-release", {"draft": "planned-feature", "revision": 1}, "planner")
        mission = self.action("draft-release", {"draft": "planned-feature", "revision": 1})
        self.assertEqual(mission["status"], "active")
        self.assertEqual(mission["orchestrator"], "orch")
        self.assertIn("verify behavior", mission["plan"])
        self.assertEqual(self.action("status")["drafts"][0]["status"], "released")
        self.assertTrue(self.action("inbox", actor="chief"))

    def test_release_requires_explicit_staffing_and_copies_it_into_the_mission(self):
        self.planner()
        self.action("draft-create", {"draft": "staffed", "objective": "Deliver feature", "criteria": "Verified", "plan": "Build and verify", "chief": "chief"})
        with self.assertRaisesRegex(ContractError, "at least one worker"):
            self.action("draft-release", {"draft": "staffed", "revision": 1})
        self.action("draft-update", {"draft": "staffed", "revision": 1, "objective": "Deliver feature", "criteria": "Verified", "plan": "Build and verify", "chief": "chief", "workers": ["worker"]})
        mission = self.action("draft-release", {"draft": "staffed", "revision": 2})
        self.assertEqual(mission["workers"], ["worker"])
        self.assertEqual(self.action("status")["drafts"][0]["workers"], ["worker"])

    def test_mission_staffing_validates_membership_and_limits_task_assignment(self):
        self.action("agent-add", {"agent": "worker-two", "role": "worker", "parent": "chief"})
        for workers in ([], ["worker", "worker"], ["chief"], ["unknown"]):
            with self.assertRaises(ContractError):
                self.action("mission-create", {"mission": "invalid", "chief": "chief", "objective": "Build", "criteria": "Verify", "workers": workers})
        self.mission()
        with self.assertRaisesRegex(ContractError, "explicitly staffed"):
            self.action("task-assign", {"task": "unapproved", "mission": "mission", "worker": "worker-two", "objective": "Work"}, "chief")
        with self.assertRaises(ContractError):
            self.action("mission-staff", {"mission": "mission", "revision": 1, "workers": ["worker", "worker-two"]}, "chief")
        self.assertEqual(self.action("mission-staff", {"mission": "mission", "revision": 1, "workers": ["worker", "worker-two"]}, "orch")["revision"], 2)
        self.action("task-assign", {"task": "approved", "mission": "mission", "worker": "worker-two", "objective": "Work"}, "chief")
        with self.assertRaises(ContractError):
            self.action("mission-staff", {"mission": "mission", "revision": 1, "workers": ["worker"]})
        before = self.action("export")
        with self.assertRaisesRegex(ContractError, "Finish"):
            self.action("mission-staff", {"mission": "mission", "revision": 2, "workers": ["worker"]})
        self.assertEqual(before, self.action("export"))

    def test_transfer_moves_staff_without_tasks_and_preserves_other_mission_ownership(self):
        self.second_chief()
        for mission in ("first", "second"):
            self.action("mission-create", {"mission": mission, "chief": "chief", "objective": "Work", "criteria": "Verify", "workers": ["worker"]})
        with self.assertRaisesRegex(ContractError, "another open mission"):
            self.action("mission-reassign", {"mission": "first", "chief": "chief-two", "revision": 1, "move_workers": True})
        with self.assertRaises(ContractError):
            self.action("agent-reparent", {"agent": "worker", "parent": "chief-two", "revision": 1})
        team = Team(self.request); team.open()
        with team.store.connect(True) as db:
            db.execute("UPDATE missions SET status='approved' WHERE id='second'")
        moved = self.action("mission-reassign", {"mission": "first", "chief": "chief-two", "revision": 1, "move_workers": True})
        self.assertEqual(moved["workers"], ["worker"])
        self.assertEqual(next(a for a in self.action("status")["agents"] if a["id"] == "worker")["parent"], "chief-two")

    def test_schema_two_backfills_only_workers_with_existing_tasks(self):
        self.mission()
        self.action("agent-add", {"agent": "idle-worker", "role": "worker", "parent": "chief"})
        team = Team(self.request); team.open()
        with team.store.connect(True) as db:
            db.execute("DROP TABLE mission_workers")
            db.execute("DROP TABLE draft_workers")
            db.execute("UPDATE settings SET value='2' WHERE key='schema'")
        self.assertEqual(self.action("status")["missions"][0]["workers"], ["worker"])

    def test_planner_cannot_create_executing_missions_or_workers(self):
        self.planner()
        with self.assertRaises(ContractError):
            self.action("mission-create", {"mission": "bypass", "chief": "chief", "objective": "Bypass planning", "criteria": "Work"}, "planner")
        with self.assertRaises(ContractError):
            self.action("agent-add", {"agent": "unapproved", "role": "worker", "parent": "chief"}, "planner")
        self.action("role-add", {"role": "product-planner", "tier": "planner", "instructions": "Plan carefully"}, "orch")
        with self.assertRaises(ContractError):
            self.action("agent-add", {"agent": "another-planner", "role": "product-planner"})

    def test_human_and_planner_cannot_overwrite_stale_draft_revisions(self):
        self.draft()
        fields = {"draft": "planned-feature", "revision": 1, "objective": "Revised export", "criteria": "Verified criteria", "plan": "Updated plan", "chief": "chief", "workers": ["worker"]}
        self.action("draft-update", fields)
        with self.assertRaises(ContractError):
            self.action("draft-update", fields, "planner")
        with self.assertRaises(ContractError):
            self.action("draft-release", {"draft": "planned-feature", "revision": 1})
        fields["revision"] = 2
        with self.assertRaises(ContractError):
            self.action("draft-update", fields, "chief")
        self.action("draft-update", fields, "planner")
        self.assertEqual(self.action("status")["drafts"][0]["revision"], 3)

    def test_incomplete_and_released_drafts_cannot_start_more_work(self):
        self.planner()
        self.action("draft-create", {"draft": "incomplete", "objective": "Discuss this goal"})
        with self.assertRaises(ContractError):
            self.action("draft-release", {"draft": "incomplete", "revision": 1})
        self.assertEqual(self.action("status")["missions"], [])
        self.action("draft-update", {"draft": "incomplete", "revision": 1, "objective": "Discuss goal", "criteria": "Tests pass", "plan": "Build and verify", "chief": "chief", "workers": ["worker"]}, "planner")
        self.action("draft-release", {"draft": "incomplete", "revision": 2})
        with self.assertRaises(ContractError):
            self.action("draft-release", {"draft": "incomplete", "revision": 2})
        self.assertEqual(len(self.action("status")["missions"]), 1)

    def test_release_collision_rolls_back_draft_and_notifications(self):
        import sqlite3
        self.draft()
        self.action("mission-create", {"mission": "planned-feature", "chief": "chief", "objective": "Existing mission", "criteria": "Existing criteria", "workers": ["worker"]})
        before = self.action("export")
        with self.assertRaises(sqlite3.IntegrityError):
            self.action("draft-release", {"draft": "planned-feature", "revision": 1})
        self.assertEqual(self.action("status")["drafts"][0]["status"], "draft")
        self.assertEqual(before, self.action("export"))

    def test_org_chart_moves_idle_workers_with_revision_checks(self):
        self.second_chief()
        moved = self.action("agent-reparent", {"agent": "worker", "parent": "chief-two", "revision": 1})
        self.assertEqual((moved["parent"], moved["revision"]), ("chief-two", 2))
        with self.assertRaises(ContractError):
            self.action("agent-reparent", {"agent": "worker", "parent": "chief", "revision": 1})
        with self.assertRaises(ContractError):
            self.action("agent-reparent", {"agent": "worker", "parent": "chief", "revision": 2}, "chief-two")
        self.assertIn("now reports to chief-two", json.dumps(self.action("inbox", actor="worker")))

    def test_org_chart_keeps_busy_workers_with_their_mission(self):
        self.second_chief()
        self.mission()
        with self.assertRaises(ContractError):
            self.action("agent-reparent", {"agent": "worker", "parent": "chief-two", "revision": 1})
        with self.assertRaises(ContractError):
            self.action("mission-reassign", {"mission": "mission", "chief": "chief-two", "revision": 1})
        self.assertEqual(self.action("status")["missions"][0]["chief"], "chief")

    def test_mission_transfer_preserves_work_and_changes_review_authority(self):
        self.second_chief()
        self.mission()
        self.action("submit", {"kind": "task", "record": "task", "revision": 1, "result": "Implemented", "evidence": "Tests pass"}, "worker")
        self.action("mission-reassign", {"mission": "mission", "chief": "chief-two", "revision": 1, "move_workers": True}, "orch")
        state = self.action("status")
        self.assertEqual(state["tasks"][0]["revision"], 3)
        self.assertEqual(state["tasks"][0]["status"], "review")
        self.assertEqual(next(agent for agent in state["agents"] if agent["id"] == "worker")["parent"], "chief-two")
        with self.assertRaises(ContractError):
            self.action("review", {"kind": "task", "record": "task", "revision": 3, "approve": True, "feedback": "Old chief"}, "chief")
        with self.assertRaises(ContractError):
            self.action("review", {"kind": "task", "record": "task", "revision": 2, "approve": True, "feedback": "Stale review"}, "chief-two")
        self.action("review", {"kind": "task", "record": "task", "revision": 3, "approve": True, "feedback": "Verified"}, "chief-two")
        self.action("submit", {"kind": "mission", "record": "mission", "revision": 2, "result": "Complete", "evidence": "Integrated checks passed"}, "chief-two")
        with self.assertRaises(ContractError):
            self.action("mission-reassign", {"mission": "mission", "chief": "chief", "revision": 3})
        self.assertEqual(self.action("review", {"kind": "mission", "record": "mission", "revision": 3, "approve": True, "feedback": "Final signoff"}, "orch")["status"], "approved")

    def test_schema_one_upgrade_preserves_existing_team_and_work(self):
        self.mission()
        team = Team(self.request);team.open()
        with team.store.connect(True) as db:
            db.execute("ALTER TABLE agents DROP COLUMN revision")
            db.execute("ALTER TABLE missions DROP COLUMN plan")
            db.execute("DROP TABLE drafts")
            db.execute("UPDATE settings SET value='1' WHERE key='schema'")
        state = self.action("status")
        self.assertEqual(state["missions"][0]["id"], "mission")
        self.assertEqual(state["missions"][0]["plan"], "")
        self.assertEqual(state["drafts"], [])
        self.assertEqual(state["agents"][0]["revision"], 1)
        self.assertEqual(len(state["tasks"]), 1)

    def test_planners_drafts_and_org_charts_are_project_scoped(self):
        self.draft()
        project = self.root / "other-project"
        project.mkdir()
        request = self.request | {"project_id": "project-two", "project_path": str(project), "state_path": str(self.root / "other-state")}
        Team(request | {"operation": "enable", "arguments": {"memory_root": str(self.memory)}}).execute()
        Team(request | {"operation": "agent-add", "arguments": {"agent": "planner", "role": "planner"}}).execute()
        other = Team(request | {"operation": "status"}).execute()
        self.assertEqual(other["drafts"], [])
        self.assertEqual(other["missions"], [])
        self.assertEqual([agent["id"] for agent in other["agents"]], ["planner"])
        original = self.action("status")
        self.assertEqual(len(original["drafts"]), 1)
        self.assertEqual(len(original["agents"]), 4)

    def mission(self):
        self.action("mission-create", {"mission": "mission", "chief": "chief", "objective": "Build feature", "criteria": "Tests pass", "workers": ["worker"]}, "orch")
        self.action("task-assign", {"task": "task", "mission": "mission", "worker": "worker", "objective": "Implement feature"}, "chief")

    def direct_worker(self, agent="direct-worker"):
        self.action("agent-add", {"agent": agent, "role": "worker", "parent": "orch"}, "orch")
        Team(self.request | {"operation": "attach", "arguments": {"agent": agent, "session_id": "session-" + agent}}).execute()
        self.clear_mail(agent)
        return agent

    def direct_mission(self, mission="direct", worker="direct-worker", task="direct-task"):
        self.direct_worker(worker)
        created = self.action("mission-create", {"mission": mission, "supervisor": "orch", "objective": "Direct work", "criteria": "Verified", "workers": [worker]}, "orch")
        self.action("task-assign", {"task": task, "mission": mission, "worker": worker, "objective": "Do direct work"}, "orch")
        return created

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

    def test_direct_mission_runs_through_orchestrator_review_and_signoff(self):
        created = self.direct_mission()
        self.assertEqual(created["supervisor"], "orch")
        self.assertEqual(created["chief"], "orch")
        self.assertEqual(created["orchestrator"], "orch")
        self.assertIn("Supervisor: orch", json.dumps(self.action("inbox", actor="direct-worker")))
        with self.assertRaises(ContractError):
            self.action("task-assign", {"task": "intrusion", "mission": "direct", "worker": "direct-worker", "objective": "Chief work"}, "chief")
        with self.assertRaises(ContractError):
            self.action("submit", {"kind": "mission", "record": "direct", "revision": 1, "result": "Done", "evidence": "Checks"}, "orch")
        self.action("submit", {"kind": "task", "record": "direct-task", "revision": 1, "result": "Done", "evidence": "Unit test passed"}, "direct-worker")
        self.assertIn("Review task direct-task", json.dumps(self.action("inbox", actor="orch")))
        with self.assertRaises(ContractError):
            self.action("review", {"kind": "task", "record": "direct-task", "revision": 1, "approve": True, "feedback": "Stale"}, "orch")
        with self.assertRaises(ContractError):
            self.action("review", {"kind": "task", "record": "direct-task", "revision": 2, "approve": True, "feedback": "Chief review"}, "chief")
        self.clear_mail("orch")
        output = Team(self.request | {"operation": "wake", "arguments": {"agent": "orch"}}).execute()
        self.assertTrue(output["needed"])
        self.action("review", {"kind": "task", "record": "direct-task", "revision": 2, "approve": False, "feedback": "Fix"}, "orch")
        self.action("submit", {"kind": "task", "record": "direct-task", "revision": 3, "result": "Fixed", "evidence": "New test"}, "direct-worker")
        self.action("review", {"kind": "task", "record": "direct-task", "revision": 4, "approve": True, "feedback": "Verified"}, "orch")
        self.assertIn("Task direct-task accepted", json.dumps(self.action("inbox", actor="direct-worker")))
        with self.assertRaises(ContractError):
            self.action("submit", {"kind": "mission", "record": "direct", "revision": 1, "result": "Done", "evidence": "Checks"}, "chief")
        self.action("submit", {"kind": "mission", "record": "direct", "revision": 1, "result": "Integrated", "evidence": "Integration test passed"}, "orch")
        self.assertIn("Review mission direct", json.dumps(self.action("inbox", actor="orch")))
        self.action("review", {"kind": "mission", "record": "direct", "revision": 2, "approve": False, "feedback": "More evidence"}, "orch")
        self.action("submit", {"kind": "mission", "record": "direct", "revision": 3, "result": "Integrated", "evidence": "Full suite passed"}, "orch")
        result = self.action("review", {"kind": "mission", "record": "direct", "revision": 4, "approve": True, "feedback": "Final signoff"}, "orch")
        self.assertEqual(result["status"], "approved")
        self.clear_mail("orch")
        output = Team(self.request | {"operation": "wake", "arguments": {"agent": "orch"}}).execute()
        self.assertFalse(output["needed"])

    def test_mission_rosters_reject_workers_outside_the_selected_supervisor(self):
        self.direct_worker()
        with self.assertRaises(ContractError):
            self.action("mission-create", {"mission": "direct", "supervisor": "orch", "objective": "Work", "criteria": "Verify", "workers": ["worker"]}, "orch")
        with self.assertRaises(ContractError):
            self.action("mission-create", {"mission": "mixed", "chief": "chief", "objective": "Work", "criteria": "Verify", "workers": ["direct-worker"]}, "orch")
        created = self.action("mission-create", {"mission": "legacy", "chief": "chief", "objective": "Work", "criteria": "Verify", "workers": ["worker"]}, "orch")
        self.assertEqual(created["supervisor"], "chief")
        with self.assertRaisesRegex(ContractError, "supervisor"):
            self.action("mission-create", {"mission": "named", "chief": "orch", "objective": "Work", "criteria": "Verify", "workers": ["direct-worker"]}, "orch")
        with self.assertRaises(ContractError):
            self.action("mission-create", {"mission": "conflict", "chief": "chief", "supervisor": "orch", "objective": "Work", "criteria": "Verify", "workers": ["worker"]}, "orch")
        with self.assertRaises(ContractError):
            self.action("mission-create", {"mission": "invalid", "supervisor": "worker", "objective": "Work", "criteria": "Verify", "workers": ["worker"]}, "orch")
        with self.assertRaises(ContractError):
            self.action("mission-create", {"mission": "unsupervised", "objective": "Work", "criteria": "Verify", "workers": ["worker"]}, "orch")
        agreed = self.action("mission-create", {"mission": "agreed", "chief": "chief", "supervisor": "chief", "objective": "Work", "criteria": "Verify", "workers": ["worker"]}, "orch")
        self.assertEqual(agreed["supervisor"], "chief")

    def test_orchestrator_creates_direct_workers_with_parent_checks(self):
        self.action("agent-add", {"agent": "direct-one", "role": "worker", "parent": "orch"}, "orch")
        with self.assertRaises(ContractError):
            self.action("agent-add", {"agent": "direct-two", "role": "worker", "parent": "orch"}, "chief")
        with self.assertRaisesRegex(ContractError, "chief or orchestrator parent"):
            self.action("agent-add", {"agent": "bad-worker", "role": "worker", "parent": "worker"})
        self.action("agent-add", {"agent": "operator-direct", "role": "worker", "parent": "orch"})
        self.action("agent-control", {"agent": "direct-one", "action": "pause"}, "orch")
        with self.assertRaisesRegex(ContractError, "direct supervisor"):
            self.action("agent-control", {"agent": "direct-one", "action": "resume"}, "chief")
        self.action("agent-control", {"agent": "direct-one", "action": "resume"}, "orch")

    def test_direct_draft_release_requires_supervisor_and_staffing(self):
        self.planner()
        self.direct_worker()
        self.action("draft-create", {"draft": "direct-plan", "objective": "Ship directly", "criteria": "Verified", "plan": "Build and verify", "supervisor": "orch", "workers": ["direct-worker"]}, "planner")
        with self.assertRaises(ContractError):
            self.action("draft-release", {"draft": "direct-plan", "revision": 1}, "orch")
        with self.assertRaises(ContractError):
            self.action("draft-release", {"draft": "direct-plan", "revision": 1}, "planner")
        mission = self.action("draft-release", {"draft": "direct-plan", "revision": 1})
        self.assertEqual(mission["supervisor"], "orch")
        self.assertEqual(mission["workers"], ["direct-worker"])
        self.assertIn("supervisor orch", json.dumps(self.action("inbox", actor="planner")))
        self.assertTrue(self.action("inbox", actor="orch"))
        self.action("draft-create", {"draft": "bare", "objective": "Discuss this goal"})
        self.action("draft-update", {"draft": "bare", "revision": 1, "objective": "Discuss goal", "criteria": "Tests pass", "plan": "Build and verify"}, "planner")
        with self.assertRaisesRegex(ContractError, "supervisor"):
            self.action("draft-release", {"draft": "bare", "revision": 2})
        with self.assertRaises(ContractError):
            self.action("draft-update", {"draft": "bare", "revision": 2, "objective": "Discuss goal", "criteria": "Tests pass", "plan": "Build and verify", "supervisor": "orch", "workers": ["worker"]}, "planner")

    def test_supervisor_changes_keep_revision_and_unfinished_work_protections(self):
        self.second_chief()
        self.direct_worker()
        moved = self.action("agent-reparent", {"agent": "worker", "parent": "orch", "revision": 1})
        self.assertEqual((moved["parent"], moved["revision"]), ("orch", 2))
        self.assertIn("now reports to orch", json.dumps(self.action("inbox", actor="worker")))
        moved = self.action("agent-reparent", {"agent": "worker", "parent": "chief", "revision": 2}, "orch")
        self.assertEqual(moved["parent"], "chief")
        with self.assertRaises(ContractError):
            self.action("agent-reparent", {"agent": "worker", "parent": "orch", "revision": 2})
        with self.assertRaises(ContractError):
            self.action("agent-reparent", {"agent": "worker", "parent": "orch", "revision": 3}, "chief")
        self.mission()
        with self.assertRaises(ContractError):
            self.action("agent-reparent", {"agent": "worker", "parent": "orch", "revision": 3})
        with self.assertRaises(ContractError):
            self.action("mission-reassign", {"mission": "mission", "revision": 1, "supervisor": "orch"})
        moved = self.action("mission-reassign", {"mission": "mission", "revision": 1, "supervisor": "orch", "move_workers": True}, "orch")
        self.assertEqual(moved["supervisor"], "orch")
        self.assertEqual(next(a for a in self.action("status")["agents"] if a["id"] == "worker")["parent"], "orch")
        self.action("submit", {"kind": "task", "record": "task", "revision": 2, "result": "Done", "evidence": "Test"}, "worker")
        with self.assertRaises(ContractError):
            self.action("review", {"kind": "task", "record": "task", "revision": 3, "approve": True, "feedback": "Old chief"}, "chief")
        self.action("review", {"kind": "task", "record": "task", "revision": 3, "approve": True, "feedback": "Verified"}, "orch")
        with self.assertRaises(ContractError):
            self.action("mission-reassign", {"mission": "mission", "revision": 1, "supervisor": "chief-two", "move_workers": True})
        self.action("mission-staff", {"mission": "mission", "revision": 2, "workers": ["direct-worker"]}, "orch")
        self.assertEqual(self.action("status")["missions"][0]["workers"], ["direct-worker"])

    def test_schema_three_upgrade_backfills_supervisor_and_keeps_chief_flows(self):
        self.mission()
        team = Team(self.request); team.open()
        with team.store.connect(True) as db:
            db.execute("ALTER TABLE missions DROP COLUMN supervisor")
            db.execute("ALTER TABLE drafts DROP COLUMN supervisor")
            db.execute("UPDATE settings SET value='3' WHERE key='schema'")
        state = self.action("status")
        self.assertEqual(state["missions"][0]["supervisor"], "chief")
        self.assertEqual(state["missions"][0]["chief"], "chief")
        self.action("submit", {"kind": "task", "record": "task", "revision": 1, "result": "Done", "evidence": "Test"}, "worker")
        self.action("review", {"kind": "task", "record": "task", "revision": 2, "approve": True, "feedback": "Verified"}, "chief")
        self.action("submit", {"kind": "mission", "record": "mission", "revision": 1, "result": "Integrated", "evidence": "Checks"}, "chief")
        result = self.action("review", {"kind": "mission", "record": "mission", "revision": 2, "approve": True, "feedback": "Final signoff"}, "orch")
        self.assertEqual(result["status"], "approved")
        self.direct_worker()
        created = self.action("mission-create", {"mission": "direct", "supervisor": "orch", "objective": "Work", "criteria": "Verify", "workers": ["direct-worker"]}, "orch")
        self.assertEqual(created["supervisor"], "orch")

    def test_block_and_unblock_follow_the_recorded_supervisor(self):
        self.direct_mission()
        self.action("block", {"kind": "task", "record": "direct-task", "revision": 1, "reason": "Need fixture"}, "direct-worker")
        with self.assertRaises(ContractError):
            self.action("unblock", {"kind": "task", "record": "direct-task", "revision": 2}, "chief")
        result = self.action("unblock", {"kind": "task", "record": "direct-task", "revision": 2}, "orch")
        self.assertEqual(result["status"], "assigned")
        with self.assertRaises(ContractError):
            self.action("block", {"kind": "mission", "record": "direct", "revision": 1, "reason": "Waiting"}, "chief")
        self.action("block", {"kind": "mission", "record": "direct", "revision": 1, "reason": "Waiting"}, "orch")
        with self.assertRaises(ContractError):
            self.action("task-assign", {"task": "second", "mission": "direct", "worker": "direct-worker", "objective": "Another"}, "orch")
        result = self.action("unblock", {"kind": "mission", "record": "direct", "revision": 2}, "orch")
        self.assertEqual(result["status"], "active")

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
