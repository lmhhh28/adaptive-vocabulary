"""E2E test runner for Adaptive Vocabulary Web modules (W-series).
Strict isolated execution, process group cleanup, non-zero exit on failure,
and result.json compatible with web_tasks.py verification.
"""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
PLAN_PATH = ROOT / "config/web-implementation-plan-v1.json"
ARTIFACTS_DIR = ROOT / "artifacts" / "web-e2e"


def get_git_revision() -> str:
    try:
        rev = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=str(ROOT), stderr=subprocess.DEVNULL)
        return rev.decode("utf-8").strip()
    except Exception:
        return "unknown"


def load_plan():
    if not PLAN_PATH.is_file():
        raise FileNotFoundError(f"Web plan not found at {PLAN_PATH}")
    return json.loads(PLAN_PATH.read_text(encoding="utf-8"))


def execute_case(case_file: Path, context: dict, timeout: float) -> dict:
    case_dir = context["case_dir"]
    context_path = case_dir / "context.json"
    result_path = case_dir / "case-result.json"
    log_path = case_dir / "case.log"
    context_path.write_text(json.dumps(context, default=str), encoding="utf-8")
    result_path.unlink(missing_ok=True)
    started = time.monotonic()
    timed_out = False

    with log_path.open("w", encoding="utf-8") as stream:
        proc = subprocess.Popen(
            [sys.executable, "-u", str(ROOT / "e2e/support/case_worker.py"),
             str(case_file), str(context_path), str(result_path)],
            stdout=stream,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        try:
            proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
        finally:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            except Exception:
                pass
            try:
                proc.wait(timeout=5)
            except Exception:
                pass

    if timed_out:
        result = {"status": "failed", "error": f"Case exceeded {timeout}s hard timeout"}
    elif proc.returncode != 0:
        result = {"status": "failed", "error": f"Case worker exited with code {proc.returncode}; see {log_path}"}
    elif not result_path.is_file():
        result = {"status": "failed", "error": "Case worker did not produce a result"}
    else:
        try:
            result = json.loads(result_path.read_text(encoding="utf-8"))
            if not isinstance(result, dict) or result.get("status") not in ("passed", "failed"):
                raise ValueError("run(context) must explicitly return status=passed or failed")
        except Exception as exc:
            result = {"status": "failed", "error": str(exc)}

    result["duration_seconds"] = round(time.monotonic() - started, 3)
    result["worker_exit_code"] = proc.returncode
    artifacts = result.get("artifacts", [])
    if str(log_path) not in artifacts:
        artifacts.insert(0, str(log_path))
    result["artifacts"] = artifacts

    result_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return result


def run_module(module_id: str, bin_path: Path, case_timeout: float) -> tuple[dict, Path]:
    plan = load_plan()
    tasks = {t["id"]: t for t in plan.get("tasks", [])}
    if module_id not in tasks:
        print(f"ERROR: Module {module_id} is not registered in web plan", file=sys.stderr)
        sys.exit(1)

    task = tasks[module_id]
    cases = task.get("acceptance", [])
    if not cases:
        print(f"ERROR: Module {module_id} has zero acceptance cases defined", file=sys.stderr)
        sys.exit(1)

    run_id = f"run_{datetime.now(timezone.utc).strftime('%Y%m%d_%H%M%S')}_{uuid.uuid4().hex[:8]}"
    module_dir = ARTIFACTS_DIR / run_id / module_id
    module_dir.mkdir(parents=True, exist_ok=True)

    start_iso = datetime.now(timezone.utc).isoformat()
    code_revision = get_git_revision()
    case_results = []
    passed_count = 0
    failed_count = 0
    skipped_count = 0
    all_artifacts = []
    started_total = time.monotonic()

    print(f"\n=== Starting Web E2E run {run_id} for {module_id} ({len(cases)} cases) ===")
    print(f"Target binary: {bin_path}")
    print(f"Code revision: {code_revision}")

    for c in cases:
        case_id = c["id"]
        case_file = ROOT / c["file"]
        case_name = case_file.stem
        scenario = c.get("scenario", "")
        case_dir = module_dir / case_id
        case_dir.mkdir(parents=True, exist_ok=True)

        if not case_file.is_file():
            print(f"-> {case_id} ({scenario}) ... FAILED (case file missing: {case_file})")
            failed_count += 1
            case_results.append({
                "id": case_id,
                "name": case_name,
                "scenario": scenario,
                "status": "failed",
                "error": f"Case file missing: {case_file}",
                "artifacts": [],
                "details": {},
            })
            continue

        context = {
            "run_id": run_id,
            "module": module_id,
            "case_id": case_id,
            "case_name": case_name,
            "scenario": scenario,
            "bin_path": bin_path,
            "case_dir": case_dir,
            "runner_py": Path(__file__).resolve(),
            "root_dir": ROOT,
            "policy_path": ROOT / "config/policy-v2.json",
        }

        print(f"-> Running {case_id}: {scenario} ...", end=" ", flush=True)
        res = execute_case(case_file, context, case_timeout)
        status = res.get("status", "failed")
        duration = res.get("duration_seconds", 0)

        if status == "passed":
            passed_count += 1
            print(f"PASSED ({duration}s)")
        else:
            failed_count += 1
            print(f"FAILED ({duration}s) - {res.get('error')}")

        case_record = {
            "id": case_id,
            "name": case_name,
            "scenario": scenario,
            "status": status,
            "duration_seconds": duration,
            "artifacts": res.get("artifacts", []),
            "details": res.get("details", {}),
        }
        if "error" in res:
            case_record["error"] = res["error"]
        case_results.append(case_record)
        all_artifacts.extend(res.get("artifacts", []))

    end_iso = datetime.now(timezone.utc).isoformat()
    overall_status = "passed" if (failed_count == 0 and skipped_count == 0 and passed_count == len(cases)) else "failed"

    result_data = {
        "run_id": run_id,
        "module": module_id,
        "design_version": plan.get("design_version", "web-design-v1.1"),
        "code_revision": code_revision,
        "status": overall_status,
        "passed": passed_count,
        "failed": failed_count,
        "skipped": skipped_count,
        "cases": case_results,
        "start_time": start_iso,
        "end_time": end_iso,
        "duration_seconds": round(time.monotonic() - started_total, 3),
        "artifacts": all_artifacts,
    }

    result_json_path = module_dir / "result.json"
    result_json_path.write_text(json.dumps(result_data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"\n=== Web E2E run finished: {overall_status} (Passed: {passed_count}/{len(cases)}, Failed: {failed_count}, Skipped: {skipped_count}) ===")
    print(f"Result saved to: {result_json_path}")
    return result_data, result_json_path


def main():
    parser = argparse.ArgumentParser(description="Adaptive Vocabulary Web E2E runner")
    parser.add_argument("module", nargs="?", default="W00", help="Module ID (e.g. W00, W01, or all)")
    parser.add_argument("--bin", default=None, help="Path to avocab binary")
    parser.add_argument("--case-timeout", type=float, default=120.0, help="Per-case timeout in seconds")
    args = parser.parse_args()

    bin_path = Path(args.bin).resolve() if args.bin else (ROOT / "bin" / "avocab")
    if not bin_path.is_file():
        print("avocab binary not found, building with make build ...", flush=True)
        subprocess.run(["make", "build"], cwd=str(ROOT), check=True)

    plan = load_plan()
    module = (args.module or "").strip()

    if module == "all":
        modules = [t["id"] for t in plan.get("tasks", [])]
    else:
        modules = [module]

    all_passed = True
    for mod in modules:
        res, _ = run_module(mod, bin_path, args.case_timeout)
        if res.get("status") != "passed":
            all_passed = False

    sys.exit(0 if all_passed else 1)


if __name__ == "__main__":
    main()
