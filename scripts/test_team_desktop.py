"""Verify native desktop launch boundaries without starting user services."""
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("team_desktop", Path(__file__).with_name("team-desktop.py"))
desktop = importlib.util.module_from_spec(spec)
spec.loader.exec_module(desktop)


class DesktopTests(unittest.TestCase):
    def test_native_bundle_paths_use_existing_forge_layout(self):
        root = Path("repository")
        for target, machine, suffix in [
            ("win32", "AMD64", "Agent Orchestrator-win32-x64/agent-orchestrator.exe"),
            ("darwin", "arm64", "Agent Orchestrator-darwin-arm64/Agent Orchestrator.app/Contents/MacOS/agent-orchestrator"),
            ("linux", "x86_64", "Agent Orchestrator-linux-x64/agent-orchestrator"),
        ]:
            self.assertEqual(desktop.packaged_executable(root, target, machine), root / "frontend" / "out" / suffix)

    def test_existing_team_state_and_native_bridge_are_preserved(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "llmwiki" / "src").mkdir(parents=True)
            repository = root / "fork"
            state = root / "state"
            with patch.dict(os.environ, {"VITE_NO_ELECTRON": "1", "VITE_AO_API_BASE_URL": "http://wrong", "AO_ALLOWED_ORIGINS": "http://browser", "ELECTRON_RUN_AS_NODE": "1"}, clear=True):
                env = desktop.desktop_environment(state, repository=repository)
            self.assertEqual(env["AO_DATA_DIR"], str(state / "data"))
            self.assertEqual(env["AO_RUN_FILE"], str(state / "running.json"))
            self.assertEqual(env["AO_TEAM_DESKTOP"], "1")
            self.assertEqual(env["AO_LLMWIKI_SOURCE"], str(root / "llmwiki" / "src"))
            for key in ("VITE_NO_ELECTRON", "VITE_AO_API_BASE_URL", "AO_ALLOWED_ORIGINS", "ELECTRON_RUN_AS_NODE"):
                self.assertNotIn(key, env)

    def test_legacy_launcher_opens_desktop_without_starting_browser_services(self):
        spec = importlib.util.spec_from_file_location("team_dev", Path(__file__).with_name("team-dev.py"))
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        with patch.object(module.sys, "argv", ["team-dev.py", "--data-dir", "chosen-state"]), \
             patch.object(module.subprocess, "run") as run, patch.object(module, "check_ports") as ports:
            module.main()
        ports.assert_not_called()
        self.assertIn("team-desktop.py", run.call_args.args[0][1])
        self.assertEqual(run.call_args.args[0][2:], ["--data-dir", "chosen-state", "--port", "43991"])

    def test_launch_delegates_daemon_and_dialogs_to_electron(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            executable = state / "desktop.exe"
            executable.touch()
            with patch.object(desktop, "packaged_executable", return_value=executable), \
                 patch.object(desktop.subprocess, "Popen") as spawn, \
                 patch.object(desktop, "build_desktop") as build, \
                 patch.object(desktop.sys, "argv", ["team-desktop.py", "--data-dir", str(state), "--port", "44001"]):
                desktop.main()
            build.assert_not_called()
            spawn.assert_called_once()
            self.assertEqual(spawn.call_args.args[0], [str(executable)])
            self.assertEqual(spawn.call_args.kwargs["env"]["AO_PORT"], "44001")
            self.assertEqual(spawn.call_args.kwargs["env"]["AO_RUN_FILE"], str(state / "running.json"))

    def test_missing_package_has_an_actionable_error_without_starting_children(self):
        with tempfile.TemporaryDirectory() as directory, \
             patch.object(desktop, "packaged_executable", return_value=Path(directory) / "missing"), \
             patch.object(desktop.subprocess, "Popen") as spawn, \
             patch.object(desktop.sys, "argv", ["team-desktop.py", "--data-dir", directory]):
            with self.assertRaisesRegex(RuntimeError, "--build"):
                desktop.main()
            spawn.assert_not_called()


if __name__ == "__main__":
    unittest.main()
