"""Launch the existing AO Electron desktop app with the fork's team runtime."""
import argparse
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys


REPOSITORY = Path(__file__).resolve().parents[1]


def packaged_executable(repository=REPOSITORY, target=None, machine=None):
    target = target or sys.platform
    machine = (machine or platform.machine()).lower()
    arch = {"amd64": "x64", "x86_64": "x64", "arm64": "arm64", "aarch64": "arm64"}.get(machine)
    if not arch or target not in {"win32", "darwin", "linux"}:
        raise RuntimeError(f"Unsupported desktop target {target}/{machine}")
    bundle = repository / "frontend" / "out" / f"Agent Orchestrator-{target}-{arch}"
    if target == "darwin":
        return bundle / "Agent Orchestrator.app" / "Contents" / "MacOS" / "agent-orchestrator"
    return bundle / ("agent-orchestrator.exe" if target == "win32" else "agent-orchestrator")


def desktop_environment(state, memory_root=None, repository=REPOSITORY):
    environment = dict(os.environ)
    tools = repository / ".cache" / "toolchains"
    cached = [tools / "go" / "bin", *sorted(tools.glob("node-*-win-x64"))]
    environment["PATH"] = os.pathsep.join([str(path) for path in cached if path.is_dir()] + [environment.get("PATH", "")])
    python = Path(sys.executable)
    if python.name.lower() == "pythonw.exe":
        python = python.with_name("python.exe")
    environment.update({"AO_DATA_DIR": str(state / "data"), "AO_RUN_FILE": str(state / "running.json"),
                        "AO_TEAM_PYTHON": str(python), "AO_TEAM_DESKTOP": "1"})
    # Electron's existing preload discovers the daemon and owns its lifecycle.
    # A browser-development override must not disable that desktop integration.
    for key in ("VITE_NO_ELECTRON", "VITE_AO_API_BASE_URL", "AO_ALLOWED_ORIGINS", "ELECTRON_RUN_AS_NODE"):
        environment.pop(key, None)
    source = repository.parent / "llmwiki" / "src"
    if source.is_dir() and not environment.get("AO_LLMWIKI_SOURCE"):
        environment["AO_LLMWIKI_SOURCE"] = str(source)
    if memory_root:
        environment["LLMWIKI_MEMORY_ROOT"] = str(memory_root.expanduser().resolve())
    return environment


def build_desktop(environment):
    node = shutil.which("node", path=environment["PATH"])
    npm = shutil.which("npm", path=environment["PATH"])
    if not node or not npm:
        raise RuntimeError("Install Node 24+ and the repository dependencies before building the desktop app")
    if os.name == "nt":
        npm_cli = Path(npm).parent / "node_modules" / "npm" / "bin" / "npm-cli.js"
        command = [node, str(npm_cli)]
    else:
        command = [npm]
    environment = dict(environment)
    environment.setdefault("AO_RELEASE_REPO", "georgeyhere/agent-orchestrator")
    print("Building the standard AO Electron package. This first build downloads its runtime resources.", flush=True)
    subprocess.run([*command, "run", "package"], cwd=REPOSITORY / "frontend", env=environment, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--build", action="store_true", help="Build through AO's existing Electron Forge pipeline")
    parser.add_argument("--build-only", action="store_true", help="Build without opening the app")
    parser.add_argument("--data-dir", type=Path, default=Path.home() / ".ao" / "team-dev")
    parser.add_argument("--memory-root", type=Path)
    parser.add_argument("--port", type=int, default=43991)
    parser.add_argument("--shortcut", action="store_true", help="Create a Windows desktop shortcut")
    arguments = parser.parse_args()
    state = arguments.data_dir.expanduser().resolve()
    state.mkdir(parents=True, exist_ok=True)
    environment = desktop_environment(state, arguments.memory_root)
    if not 1 <= arguments.port <= 65535:
        raise RuntimeError("Choose a port between 1 and 65535")
    environment["AO_PORT"] = str(arguments.port)
    if arguments.build or arguments.build_only:
        build_desktop(environment)
    executable = packaged_executable()
    if not executable.is_file():
        raise RuntimeError("Desktop package is not built yet. Run this launcher with --build first")
    if arguments.shortcut:
        if os.name != "nt":
            raise RuntimeError("--shortcut currently creates a Windows desktop shortcut")
        pythonw = Path(sys.executable).with_name("pythonw.exe")
        if not pythonw.is_file():
            raise RuntimeError("The selected Python environment needs pythonw.exe for the desktop shortcut")
        options = {"data_dir": str(state), "port": arguments.port,
                   "memory_root": str(arguments.memory_root.expanduser().resolve()) if arguments.memory_root else None}
        config = state / "desktop-launch.json"
        config.write_text(json.dumps(options), encoding="utf-8")
        subprocess.run(["powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File",
                        str(REPOSITORY / "scripts" / "team-shortcut.ps1"), "-PythonExecutable", str(pythonw),
                        "-Launcher", str(Path(__file__).resolve()), "-Config", str(config), "-AppExecutable", str(executable)], check=True,
                       creationflags=subprocess.CREATE_NO_WINDOW)
    if arguments.build_only:
        return
    print(f"Opening AO desktop. Team state is at {state}", flush=True)
    # Do not start a parallel daemon or a Vite server here. Electron already
    # implements native dialogs, process supervision, discovery, and shutdown.
    subprocess.Popen([str(executable)], cwd=REPOSITORY / "frontend", env=environment,
                     **({"creationflags": subprocess.CREATE_NO_WINDOW} if os.name == "nt" else {"start_new_session": True}))


if __name__ == "__main__":
    try:
        # A shortcut pins its chosen data directory without depending on cwd.
        if len(sys.argv) == 3 and sys.argv[1] == "--config":
            settings = json.loads(Path(sys.argv[2]).read_text(encoding="utf-8"))
            sys.argv = [sys.argv[0], "--data-dir", settings["data_dir"], "--port", str(settings["port"])]
            if settings.get("memory_root"):
                sys.argv += ["--memory-root", settings["memory_root"]]
        main()
    except Exception as error:
        if os.name == "nt" and Path(sys.executable).name.lower() == "pythonw.exe":
            import ctypes
            ctypes.windll.user32.MessageBoxW(None, str(error), "Agent Teams could not start", 0x10)
        else:
            raise
