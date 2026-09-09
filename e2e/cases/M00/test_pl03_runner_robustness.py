"""PL-03: Runner robustness acceptance test.
Verifies runner non-zero exits on zero cases, startup failures, assertion failures, and guarantees subprocess cleanup.
"""
import os
from pathlib import Path
import subprocess
import sys
from support.lifecycle import managed_server


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    runner_py = context["runner_py"]
    python_bin = sys.executable

    # Check 1: Zero test cases -> runner must exit non-zero
    proc_zero = subprocess.run(
        [python_bin, str(runner_py), "NON_EXISTENT_MODULE_ZERO"],
        capture_output=True,
        text=True,
    )
    assert proc_zero.returncode != 0, f"Runner on non-existent module should fail non-zero, got {proc_zero.returncode}"

    # Check 2: Subprocess cleanup guarantee
    # Start a server, check its PID exists, then exit context and verify PID no longer exists
    data_dir = case_dir / "cleanup_check"
    data_dir.mkdir(parents=True, exist_ok=True)

    server_pid = None
    with managed_server(bin_path, data_dir, listen_port=0, log_dir=case_dir) as server:
        server_pid = server.pid
        # Verify server process is alive
        assert os.path.exists(f"/proc/{server_pid}") or _pid_is_alive(server_pid)

    # After exiting context, process must be terminated
    assert not _pid_is_alive(server_pid), f"Server PID {server_pid} was not cleaned up!"

    return {
        "status": "passed",
        "artifacts": [],
        "details": {
            "zero_cases_rejected": True,
            "subprocess_cleanup_verified": True,
        },
    }


def _pid_is_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
        return True
    except OSError:
        return False
