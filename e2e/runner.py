"""E2E test runner for Adaptive Vocabulary modules.
Guarantees isolated execution, subprocess cleanup, result.json output, and non-zero exit on failures.
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
CASES_DIR = ROOT / "e2e" / "cases"
ARTIFACTS_DIR = ROOT / "artifacts" / "e2e"


def execute_case(case_file: Path, context: dict, timeout: float) -> dict:
    """Bound imports and run() together, then clean the entire case process group."""
    case_dir = context["case_dir"]
    context_path = case_dir / "context.json"
    result_path = case_dir / "case-result.json"
    log_path = case_dir / "case.log"
    context_path.write_text(json.dumps(context, default=str))
    result_path.unlink(missing_ok=True)
    started = time.monotonic()
    timed_out = False
    with log_path.open("w") as stream:
        proc = subprocess.Popen([sys.executable, "-u", str(ROOT / "e2e/support/case_worker.py"),
                                 str(case_file), str(context_path), str(result_path)],
                                stdout=stream, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
        finally:
            # Also clean descendants after an assertion failure or early worker exit.
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            proc.wait(timeout=5)
    if timed_out:
        result = {"status": "failed", "error": f"Case exceeded {timeout}s hard timeout"}
    elif proc.returncode != 0:
        result = {"status": "failed", "error": f"Case worker exited with code {proc.returncode}; see {log_path}"}
    elif not result_path.is_file():
        result = {"status": "failed", "error": "Case worker did not produce a result"}
    else:
        try:
            result = json.loads(result_path.read_text())
            if not isinstance(result, dict) or result.get("status") not in ("passed", "failed"):
                raise ValueError("run(context) must explicitly return status=passed or failed")
            if not isinstance(result.get("artifacts", []), list) or not isinstance(result.get("details", {}), dict):
                raise ValueError("Invalid artifacts or details in case result")
        except (ValueError, TypeError) as exc:
            result = {"status": "failed", "error": str(exc)}
    result["duration_seconds"] = round(time.monotonic() - started, 3)
    result["worker_exit_code"] = proc.returncode
    result["artifacts"] = [str(log_path)] + result.get("artifacts", [])
    # Persist failures caused by timeout/exit too, so every case has an explicit result.
    result_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    return result


def discover_cases(module: str) -> list[tuple[str, Path]]:
    if module.lower() == "all":
        cases = []
        for mod_dir in sorted(CASES_DIR.glob("M*")):
            if mod_dir.is_dir():
                for c in sorted(mod_dir.glob("test_*.py")):
                    cases.append((mod_dir.name, c))
        return cases
    mod_dir = CASES_DIR / module
    if not mod_dir.is_dir():
        return []
    return [(module, c) for c in sorted(mod_dir.glob("test_*.py"))]


def main():
    parser = argparse.ArgumentParser(description="Run E2E test acceptance suites")
    parser.add_argument("module", nargs="?", default="all", help="Module ID to test (e.g. M00, M01, or all)")
    parser.add_argument("--bin", default=None, help="Path to avocab binary")
    parser.add_argument("--case-timeout", type=float, default=120,
                        help="Hard timeout per case, including import (seconds; default 120)")
    args = parser.parse_args()
    if args.case_timeout <= 0 or not args.case_timeout < float("inf"):
        parser.error("--case-timeout must be a positive finite number")
    if sys.flags.optimize:
        parser.error("Optimized Python disables acceptance assertions; unset PYTHONOPTIMIZE")

    module = args.module
    cases = discover_cases(module)
    if not cases:
        print(f"ERROR: Zero test cases found for module {module}. Runner requires implemented tests.", file=sys.stderr)
        sys.exit(1)

    bin_path = Path(args.bin).resolve() if args.bin else (ROOT / "bin" / "avocab-e2e")
    if not args.bin and not bin_path.is_file():
        bin_path = ROOT / "bin" / "avocab"
    if not bin_path.is_file():
        print(f"ERROR: Binary not found at {bin_path}. Run 'make build' first.", file=sys.stderr)
        sys.exit(1)

    run_id = f"run_{datetime.now(timezone.utc).strftime('%Y%m%d_%H%M%S')}_{uuid.uuid4().hex[:8]}"
    run_dir = ARTIFACTS_DIR / run_id / module
    run_dir.mkdir(parents=True, exist_ok=True)

    start_iso = datetime.now(timezone.utc).isoformat()
    case_results = []
    all_artifact_paths = []
    exit_codes = []
    passed_count = 0
    failed_count = 0
    skipped_count = 0

    print(f"=== Starting E2E run {run_id} for {module} ({len(cases)} cases) ===")

    for mod_name, case_file in cases:
        case_name = case_file.stem
        case_dir = run_dir / mod_name / case_name
        case_dir.mkdir(parents=True, exist_ok=True)

        context = {
            "run_id": run_id,
            "module": mod_name,
            "case_name": case_name,
            "bin_path": bin_path,
            "case_dir": case_dir,
            "runner_py": Path(__file__).resolve(),
            "root_dir": ROOT,
            "policy_path": ROOT / "config/policy-v2.json",
        }

        t0 = time.time()
        print(f"-> Running {case_name} ...", end=" ", flush=True)

        try:
            res = execute_case(case_file, context, args.case_timeout)
            duration = round(time.time() - t0, 3)

            status = res["status"]
            artifacts = res.get("artifacts", [])
            details = res.get("details", {})

            if status == "passed":
                passed_count += 1
                exit_codes.append(0)
                print(f"PASSED ({duration}s)")
            else:
                failed_count += 1
                exit_codes.append(1)
                print(f"FAILED ({duration}s)")

            case_results.append({
                "name": case_name,
                "status": status,
                "duration_seconds": duration,
                "artifacts": artifacts,
                "details": details,
                "error": res.get("error"),
                "worker_exit_code": res["worker_exit_code"],
            })
            all_artifact_paths.extend(artifacts)

        except Exception as e:
            import traceback
            traceback.print_exc()
            duration = round(time.time() - t0, 3)
            failed_count += 1
            exit_codes.append(1)
            print(f"ERROR ({duration}s): {e}")
            case_results.append({
                "name": case_name,
                "status": "failed",
                "duration_seconds": duration,
                "error": str(e),
            })

    end_iso = datetime.now(timezone.utc).isoformat()
    overall_status = "passed" if (failed_count == 0 and skipped_count == 0 and passed_count > 0) else "failed"

    result_data = {
        "run_id": run_id,
        "module": module,
        "status": overall_status,
        "cases": case_results,
        "passed": passed_count,
        "failed": failed_count,
        "skipped": skipped_count,
        "versions": {
            "contract": "1.1.0",
            "design": "1.1.0",
            "python": sys.version.split()[0],
        },
        "start_time": start_iso,
        "end_time": end_iso,
        "exit_codes": exit_codes,
        "case_timeout_seconds": args.case_timeout,
        "artifact_paths": all_artifact_paths,
    }

    result_json_path = run_dir / "result.json"
    result_json_path.write_text(json.dumps(result_data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"=== E2E run finished: {overall_status} (Passed: {passed_count}, Failed: {failed_count}, Skipped: {skipped_count}) ===")
    print(f"Result summary saved to {result_json_path}")

    if overall_status != "passed":
        sys.exit(1)
    sys.exit(0)


if __name__ == "__main__":
    main()
