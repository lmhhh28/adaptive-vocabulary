"""Subprocess lifecycle manager for starting, polling, and stopping avocab processes."""
import json
from pathlib import Path
import signal
import subprocess
import time
from typing import Generator
from contextlib import contextmanager
import httpx


class ManagedProcess:
    def __init__(self, proc: subprocess.Popen, ready_data: dict, log_path: Path):
        self.proc = proc
        self.ready_data = ready_data
        self.log_path = log_path
        self.base_url = ready_data.get("base_url")
        self.pid = proc.pid

    def stop(self, timeout: float = 5.0):
        if self.proc.poll() is not None:
            return
        try:
            self.proc.send_signal(signal.SIGTERM)
            self.proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait(timeout=2.0)
        except ProcessLookupError:
            self.proc.wait(timeout=2.0)


def start_server(
    bin_path: Path,
    data_dir: Path,
    listen_port: int = 0,
    policy_path: Path | None = None,
    seed: str | None = None,
    log_dir: Path | None = None,
    startup_timeout: float = 10.0,
) -> ManagedProcess:
    if not bin_path.is_file():
        raise FileNotFoundError(f"Binary not found: {bin_path}")

    root = bin_path.resolve().parents[1]
    if policy_path is None:
        policy_path = root / "config/policy-v2.json"
    if log_dir is None:
        log_dir = data_dir

    log_dir.mkdir(parents=True, exist_ok=True)
    log_path = log_dir / "server.log"

    cmd = [
        str(bin_path),
        "serve",
        "--data-dir",
        str(data_dir),
        "--listen",
        f"127.0.0.1:{listen_port}",
        "--policy",
        str(policy_path),
    ]
    if seed:
        cmd.extend(["--seed", seed])

    # A regular file cannot block waiting for a newline, unlike PIPE.readline().
    # The child owns its descriptor; the parent closes all handles deterministically.
    with log_path.open("w", encoding="utf-8") as log_file:
        proc = subprocess.Popen(cmd, stdout=log_file, stderr=subprocess.STDOUT)
    try:
        ready_data = None
        deadline = time.monotonic() + startup_timeout
        pending = ""
        with log_path.open(encoding="utf-8", errors="replace") as reader:
            while time.monotonic() < deadline:
                pending += reader.read()
                while "\n" in pending:
                    line, pending = pending.split("\n", 1)
                    try:
                        data = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    if isinstance(data, dict) and data.get("ready") and data.get("base_url"):
                        ready_data = data
                        break
                if ready_data:
                    break
                if proc.poll() is not None:
                    raise RuntimeError(f"Server process terminated unexpectedly with code {proc.returncode}: "
                                       + log_path.read_text(errors="replace"))
                time.sleep(min(0.02, max(0, deadline - time.monotonic())))
        if ready_data is None:
            raise TimeoutError(f"Server did not emit ready signal within {startup_timeout}s")

        with httpx.Client(trust_env=False) as client:
            while time.monotonic() < deadline and proc.poll() is None:
                try:
                    response = client.get(f"{ready_data['base_url']}/api/v1/health",
                                          timeout=min(1.0, max(0.001, deadline - time.monotonic())))
                    if response.status_code == 200:
                        return ManagedProcess(proc, ready_data, log_path)
                except httpx.HTTPError:
                    pass
                time.sleep(min(0.02, max(0, deadline - time.monotonic())))
        raise TimeoutError(f"Health check failed within startup timeout {startup_timeout}s")
    except BaseException:
        if proc.poll() is None:
            proc.kill()
        proc.wait(timeout=2.0)
        raise


@contextmanager
def managed_server(
    bin_path: Path,
    data_dir: Path,
    listen_port: int = 0,
    policy_path: Path | None = None,
    seed: str | None = None,
    log_dir: Path | None = None,
    startup_timeout: float = 10.0,
) -> Generator[ManagedProcess, None, None]:
    proc = start_server(
        bin_path=bin_path,
        data_dir=data_dir,
        listen_port=listen_port,
        policy_path=policy_path,
        seed=seed,
        log_dir=log_dir,
        startup_timeout=startup_timeout,
    )
    try:
        yield proc
    finally:
        proc.stop()
