"""Subprocess lifecycle manager for starting, polling, and stopping avocab processes."""
import json
import os
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
        except Exception:
            self.proc.kill()


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
    log_file = log_path.open("w", encoding="utf-8")

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

    proc = subprocess.Popen(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
    )

    ready_data = None
    start_time = time.time()

    while time.time() - start_time < startup_timeout:
        if proc.poll() is not None:
            log_file.close()
            output = log_path.read_text(encoding="utf-8") if log_path.is_file() else ""
            raise RuntimeError(f"Server process terminated unexpectedly with code {proc.returncode}: {output}")

        line = proc.stdout.readline()
        if line:
            log_file.write(line)
            log_file.flush()
            line_str = line.strip()
            if line_str.startswith("{") and '"ready"' in line_str:
                try:
                    data = json.loads(line_str)
                    if data.get("ready"):
                        ready_data = data
                        break
                except json.JSONDecodeError:
                    pass
        else:
            time.sleep(0.05)

    if ready_data is None:
        proc.kill()
        proc.wait(timeout=2.0)
        log_file.close()
        raise TimeoutError(f"Server did not emit ready signal within {startup_timeout}s")

    # Start a background logger thread or continue redirecting
    def drain_logs():
        try:
            for remaining in proc.stdout:
                log_file.write(remaining)
                log_file.flush()
        except Exception:
            pass
        finally:
            log_file.close()

    import threading
    t = threading.Thread(target=drain_logs, daemon=True)
    t.start()

    # Poll health endpoint
    base_url = ready_data["base_url"]
    health_ok = False
    deadline = time.time() + 5.0
    while time.time() < deadline:
        try:
            resp = httpx.get(f"{base_url}/api/v1/health", timeout=1.0)
            if resp.status_code == 200:
                health_ok = True
                break
        except Exception:
            time.sleep(0.05)

    if not health_ok:
        proc.kill()
        proc.wait(timeout=2.0)
        raise RuntimeError("Health check failed after ready signal")

    return ManagedProcess(proc, ready_data, log_path)


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
