#!/usr/bin/env python3
"""Runtime supervisor for web modules in Adaptive Vocabulary.
Starts avocab server for a specified module with dependency checking and process isolation.
"""
import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
PLAN_PATH = ROOT / "config/web-implementation-plan-v1.json"


def load_plan():
    if not PLAN_PATH.is_file():
        raise FileNotFoundError(f"Web plan not found at {PLAN_PATH}")
    return json.loads(PLAN_PATH.read_text(encoding="utf-8"))


def validate_module_deps(plan: dict, module_id: str):
    tasks = {t["id"]: t for t in plan.get("tasks", [])}
    if module_id not in tasks:
        raise ValueError(f"Module '{module_id}' is not registered in web plan")

    task = tasks[module_id]
    deps = task.get("depends_on", [])
    missing_deps = [d for d in deps if tasks.get(d, {}).get("status") != "complete"]
    if missing_deps:
        raise RuntimeError(
            f"Module '{module_id}' has incomplete dependencies: {', '.join(missing_deps)}. "
            f"Dependencies must be 'complete' before running {module_id}."
        )
    return task


def main():
    parser = argparse.ArgumentParser(description="Run Adaptive Vocabulary web module supervisor")
    parser.add_argument("--module", required=True, help="Module ID (e.g. W00, W01, W02)")
    parser.add_argument("--data-dir", default=None, help="Data directory (defaults to artifacts/manual/<MODULE>)")
    parser.add_argument("--listen", default="127.0.0.1:0", help="Listen address (default 127.0.0.1:0)")
    parser.add_argument("--seed", default="core", help="Seed dataset (default core)")
    parser.add_argument("--policy", default=str(ROOT / "config/policy-v2.json"), help="Policy JSON path")
    args = parser.parse_args()

    plan = load_plan()
    task = validate_module_deps(plan, args.module)

    data_dir = Path(args.data_dir).resolve() if args.data_dir else (ROOT / f"artifacts/manual/{args.module}")
    data_dir.mkdir(parents=True, exist_ok=True)

    bin_path = ROOT / "bin" / "avocab"
    if not bin_path.is_file():
        print("Building avocab binary ...", flush=True)
        subprocess.run(["make", "build"], cwd=str(ROOT), check=True)

    dist_dir = ROOT / "web/frontend/dist"
    if not dist_dir.is_dir():
        print("Building web frontend ...", flush=True)
        subprocess.run(["npm", "run", "build"], cwd=str(ROOT / "web/frontend"), check=True)

    cmd = [
        str(bin_path),
        "serve",
        "--data-dir", str(data_dir),
        "--listen", args.listen,
        "--policy", str(args.policy),
    ]
    if args.seed:
        cmd.extend(["--seed", args.seed])

    log_path = data_dir / "server.log"
    print(f"Starting avocab server for {args.module} ...", flush=True)

    with open(log_path, "w", encoding="utf-8") as stream:
        proc = subprocess.Popen(cmd, stdout=stream, stderr=subprocess.STDOUT, start_new_session=True)

    ready_data = None
    deadline = time.monotonic() + 10.0
    pending = ""

    with open(log_path, "r", encoding="utf-8", errors="replace") as reader:
        while time.monotonic() < deadline:
            pending += reader.read()
            while "\n" in pending:
                line, pending = pending.split("\n", 1)
                line = line.strip()
                if line.startswith("{") and line.endswith("}"):
                    try:
                        data = json.loads(line)
                        if data.get("ready") is True:
                            ready_data = data
                            break
                    except Exception:
                        pass
            if ready_data:
                break
            if proc.poll() is not None:
                break
            time.sleep(0.05)

    if not ready_data:
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except Exception:
            pass
        print(f"ERROR: Server failed to start. Logs in {log_path}:", file=sys.stderr)
        if log_path.is_file():
            print(log_path.read_text(encoding="utf-8", errors="replace"), file=sys.stderr)
        sys.exit(1)

    base_url = ready_data["base_url"]
    entry_path = task.get("entry_path", "/")
    full_url = f"{base_url}{entry_path}"

    print(json.dumps({
        "status": "ready",
        "module": args.module,
        "pid": proc.pid,
        "base_url": base_url,
        "entry_url": full_url,
        "data_dir": str(data_dir),
    }, indent=2))
    print(f"\n=======================================================")
    print(f"  Module: {args.module} ({task.get('title')})")
    print(f"  Entry URL: {full_url}")
    print(f"  Server PID: {proc.pid}")
    print(f"  Data Dir: {data_dir}")
    print(f"=======================================================\n")
    print("Press Ctrl+C to terminate the server.\n", flush=True)

    def shutdown(sig, frame):
        print("\nShutting down server ...", flush=True)
        try:
            os.killpg(proc.pid, signal.SIGTERM)
            proc.wait(timeout=3.0)
        except Exception:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except Exception:
                pass
        sys.exit(0)

    signal.signal(signal.SIGINT, shutdown)
    signal.signal(signal.SIGTERM, shutdown)

    try:
        proc.wait()
    except KeyboardInterrupt:
        shutdown(None, None)


if __name__ == "__main__":
    main()
