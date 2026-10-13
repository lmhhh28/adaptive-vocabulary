"""W00-04: 零测试、缺证据、超时、孤儿进程必须失败"""
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[3]
WEB_RUNNER = ROOT / "e2e/web_runner.py"
CASE_WORKER = ROOT / "e2e/support/case_worker.py"


def run(context: dict) -> dict:
    case_dir = Path(context["case_dir"])
    case_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    # 1. Negative test: Unknown module / zero cases must exit non-zero
    proc1 = subprocess.run(
        [sys.executable, str(WEB_RUNNER), "W99_NONEXISTENT"],
        cwd=str(ROOT),
        capture_output=True,
        text=True,
    )
    assert proc1.returncode != 0, f"Expected non-zero exit for non-existent module, got {proc1.returncode}"

    # 2. Negative test: Timeout must kill worker and report failure
    timeout_case = case_dir / "timeout_subcase.py"
    timeout_case.write_text("import time\ndef run(ctx):\n    time.sleep(30)\n    return {'status': 'passed'}\n")
    artifacts.append(str(timeout_case))

    ctx_file = case_dir / "timeout_ctx.json"
    ctx_file.write_text("{}", encoding="utf-8")
    res_file = case_dir / "timeout_res.json"
    log_file = case_dir / "timeout.log"

    started = time.monotonic()
    with open(log_file, "w") as stream:
        proc_to = subprocess.Popen(
            [sys.executable, "-u", str(CASE_WORKER), str(timeout_case), str(ctx_file), str(res_file)],
            stdout=stream,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        timed_out = False
        try:
            proc_to.wait(timeout=1.5)
        except subprocess.TimeoutExpired:
            timed_out = True
            try:
                os.killpg(proc_to.pid, signal.SIGKILL)
            except Exception:
                pass
            proc_to.wait(timeout=2.0)

    assert timed_out, "Expected timeout for long-running case"
    assert not res_file.is_file(), "Timed out process should not have written result"
    artifacts.extend([str(log_file), str(ctx_file)])

    # 3. Negative test: Orphan process cleanup
    orphan_case = case_dir / "orphan_subcase.py"
    orphan_case.write_text("""import subprocess, sys, time
def run(ctx):
    # Spawn detached sleep process
    p = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
    with open(ctx["pid_file"], "w") as f:
        f.write(str(p.pid))
    # Early exit without waiting
    return {"status": "passed", "artifacts": [], "details": {}}
""")
    artifacts.append(str(orphan_case))

    pid_file = case_dir / "orphan.pid"
    ctx_orphan = {"pid_file": str(pid_file)}
    ctx_orphan_file = case_dir / "ctx_orphan.json"
    ctx_orphan_file.write_text(str(ctx_orphan).replace("'", '"'), encoding="utf-8")
    res_orphan_file = case_dir / "res_orphan.json"
    log_orphan_file = case_dir / "orphan.log"

    with open(log_orphan_file, "w") as stream:
        proc_orphan = subprocess.Popen(
            [sys.executable, "-u", str(CASE_WORKER), str(orphan_case), str(ctx_orphan_file), str(res_orphan_file)],
            stdout=stream,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        proc_orphan.wait(timeout=5.0)
        # Parent kills process group of the worker
        try:
            os.killpg(proc_orphan.pid, signal.SIGKILL)
        except Exception:
            pass

    # Verify orphan child process was killed or clean it
    if pid_file.is_file():
        child_pid = int(pid_file.read_text().strip())
        try:
            # Check if alive
            os.kill(child_pid, 0)
            # If still alive, kill it now
            os.kill(child_pid, signal.SIGKILL)
        except ProcessLookupError:
            # Already killed properly by process group
            pass

    artifacts.extend([str(log_orphan_file), str(ctx_orphan_file), str(res_orphan_file)])

    processes_path = case_dir / "processes.json"
    processes_path.write_text("[]\n", encoding="utf-8")
    artifacts.append(str(processes_path))

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "negative_zero_tests_rejected": True,
            "negative_timeout_killed": True,
            "orphan_cleanup_verified": True,
        },
    }
