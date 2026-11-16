"""Run the fork's browser GUI and daemon with an independent local data directory."""
import argparse
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import time
from urllib.error import URLError
from urllib.request import Request, urlopen
import webbrowser


def wait_ready(url, process, timeout=60, identity=None):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"Service exited with {process.returncode}. Check the printed log directory")
        try:
            with urlopen(url, timeout=2) as response:
                if response.status == 200 and (identity is None or identity()):
                    return
        except (URLError, TimeoutError):
            pass
        time.sleep(0.3)
    raise RuntimeError(f"Service did not become ready at {url}")


def owns_runfile(path, process, port):
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
        return value.get("pid") == process.pid and value.get("port") == port
    except (OSError, ValueError):
        return False


def check_ports(ports):
    if len(set(ports)) != len(ports) or any(port < 1 or port > 65535 for port in ports):
        raise RuntimeError("Choose two distinct ports between 1 and 65535")
    for port in ports:
        with socket.socket() as probe:
            try:
                probe.bind(("127.0.0.1", port))
            except OSError as exc:
                raise RuntimeError(f"Port {port} is already in use. Choose another port") from exc


def stop(process):
    if process.poll() is not None:
        return
    if os.name == "nt":
        subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], capture_output=True)
    else:
        os.killpg(process.pid, signal.SIGTERM)
    process.wait(timeout=10)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data-dir", type=Path, default=Path.home() / ".ao" / "team-dev")
    parser.add_argument("--port", type=int, default=43991)
    parser.add_argument("--gui-port", type=int, default=43992)
    parser.add_argument("--memory-root", type=Path)
    parser.add_argument("--no-browser", action="store_true")
    parser.add_argument("--skip-build", action="store_true")
    arguments = parser.parse_args()
    check_ports([arguments.port, arguments.gui_port])
    if sys.version_info < (3, 11):
        raise RuntimeError("Use Python 3.11+ for the team runtime and llmwiki")
    repository = Path(__file__).resolve().parents[1]
    environment = dict(os.environ)
    tools = repository / ".cache" / "toolchains"
    cached = [tools / "go" / "bin", *sorted(tools.glob("node-*-win-x64"))]
    environment["PATH"] = os.pathsep.join([str(path) for path in cached if path.is_dir()] + [environment.get("PATH", "")])
    node = shutil.which("node", path=environment["PATH"])
    go = shutil.which("go", path=environment["PATH"])
    if not node or (not go and not arguments.skip_build):
        raise RuntimeError("Install Node 24+ and Go 1.27.4+, or place portable tools in .cache/toolchains")
    vite = repository / "frontend" / "node_modules" / "vite" / "bin" / "vite.js"
    if not vite.exists():
        raise RuntimeError("Install the repository, frontend, and packages/product-ui dependencies as described in docs/team-workflow.md")
    executable = repository / ".cache" / "bin" / ("ao.exe" if os.name == "nt" else "ao")
    executable.parent.mkdir(parents=True, exist_ok=True)
    if not arguments.skip_build:
        subprocess.run([go, "build", "-o", str(executable), "./cmd/ao"], cwd=repository / "backend", env=environment, check=True)
    if not executable.is_file():
        raise RuntimeError("Build the daemon before using --skip-build")
    state = arguments.data_dir.expanduser().resolve()
    state.mkdir(parents=True, exist_ok=True)
    environment.update({"AO_DATA_DIR": str(state / "data"), "AO_RUN_FILE": str(state / "running.json"),
                        "AO_PORT": str(arguments.port), "AO_TEAM_PYTHON": sys.executable,
                        "AO_ALLOWED_ORIGINS": f"http://127.0.0.1:{arguments.gui_port}",
                        "VITE_NO_ELECTRON": "1", "VITE_AO_API_BASE_URL": f"http://127.0.0.1:{arguments.port}"})
    source = repository.parent / "llmwiki" / "src"
    if source.is_dir() and not environment.get("AO_LLMWIKI_SOURCE"):
        environment["AO_LLMWIKI_SOURCE"] = str(source)
    if arguments.memory_root:
        environment["LLMWIKI_MEMORY_ROOT"] = str(arguments.memory_root.expanduser().resolve())
    options = {"creationflags": subprocess.CREATE_NO_WINDOW} if os.name == "nt" else {"start_new_session": True}
    children = []
    daemon = None
    runfile = state / "running.json"
    print(f"Team development logs and state at {state}", flush=True)
    try:
        with (state / "daemon.log").open("a", encoding="utf-8") as daemon_log, (state / "gui.log").open("a", encoding="utf-8") as gui_log:
            daemon = subprocess.Popen([str(executable), "daemon"], cwd=repository, env=environment, stdout=daemon_log, stderr=daemon_log, **options)
            children.append(daemon)
            wait_ready(f"http://127.0.0.1:{arguments.port}/readyz", daemon,
                       identity=lambda: owns_runfile(runfile, daemon, arguments.port))
            gui = subprocess.Popen([node, str(vite), "--config", "vite.renderer.config.ts", "--host", "127.0.0.1", "--port", str(arguments.gui_port), "--strictPort"], cwd=repository / "frontend", env=environment, stdout=gui_log, stderr=gui_log, **options)
            children.append(gui)
            url = f"http://127.0.0.1:{arguments.gui_port}/teams"
            wait_ready(url, gui)
            print(f"Project teams at {url}", flush=True)
            if not arguments.no_browser:
                webbrowser.open(url)
            while all(child.poll() is None for child in children):
                time.sleep(0.5)
    finally:
        if daemon is not None and daemon.poll() is None and owns_runfile(runfile, daemon, arguments.port):
            try:
                with urlopen(Request(f"http://127.0.0.1:{arguments.port}/shutdown", data=b""), timeout=3):
                    pass
                daemon.wait(timeout=15)
            except (OSError, subprocess.TimeoutExpired):
                pass
        for child in reversed(children):
            stop(child)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
