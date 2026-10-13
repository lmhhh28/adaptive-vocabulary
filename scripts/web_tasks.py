"""Validate and inspect the incremental Web plan. This tool never runs task commands."""

import argparse
import json
from pathlib import Path, PurePosixPath
import re
import sys


ROOT = Path(__file__).resolve().parents[1]
PLAN_PATH = ROOT / "config/web-implementation-plan-v1.json"
STATUS_VALUES = {"planned", "in_progress", "complete"}
CASE_KINDS = {"browser", "api", "browser_worker", "harness"}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def local_path(value):
    require(isinstance(value, str) and value, "Empty or non-string local path")
    path = PurePosixPath(value)
    require(not path.is_absolute() and ".." not in path.parts and str(path) != ".",
            "Path must stay inside the repository: " + value)
    require(not any(char in value for char in "*?[]\\"), "Use literal ownership/reference paths: " + value)
    require((ROOT / path).resolve().is_relative_to(ROOT.resolve()), "Path resolves outside repository: " + value)
    return str(path)


def check_completed(task, tasks, plan):
    if task["status"] != "complete":
        return
    require(task["commands_available_now"], task["id"] + " complete but commands unavailable")
    require(all(tasks[dep]["status"] == "complete" for dep in task["depends_on"]),
            task["id"] + " complete with unfinished dependencies")
    for case in task["acceptance"]:
        require((ROOT / case["file"]).is_file(), "Completed task missing case: " + case["file"])
    verification = task.get("verification")
    require(isinstance(verification, dict), task["id"] + " requires verification metadata")
    result_path = local_path(verification.get("result_path"))
    require(result_path.startswith(plan["acceptance_root"].rstrip("/") + "/"),
            "Verification must reference a Web E2E result")
    result = json.loads((ROOT / result_path).read_text(encoding="utf-8"))
    require(result.get("module") == task["id"] and result.get("design_version") == plan["design_version"],
            "Verification scope/version mismatch: " + task["id"])
    require(result.get("status") == "passed" and result.get("failed") == 0 and result.get("skipped") == 0,
            "Verification did not pass all requirements: " + task["id"])
    expected = {case["id"] for case in task["acceptance"]}
    observed = result.get("cases", [])
    require(isinstance(observed, list) and all(isinstance(case, dict) for case in observed),
            "Invalid case result list: " + task["id"])
    require(len(observed) == len(expected) and {case.get("id") for case in observed} == expected,
            "Verification has missing/duplicate/unexpected cases: " + task["id"])
    require(all(case.get("status") == "passed" for case in observed) and result.get("passed") == len(expected),
            "Not all registered cases passed: " + task["id"])
    require(verification.get("code_revision") and result.get("code_revision") == verification["code_revision"],
            "Verification code revision missing or inconsistent: " + task["id"])


def load_plan():
    plan = json.loads(PLAN_PATH.read_text(encoding="utf-8"))
    require(plan.get("schema_version") == 1, "Unsupported plan schema")
    require(isinstance(plan.get("design_version"), str) and plan["design_version"], "Missing design version")
    for field in ("implementation_guide", "contract_guide", "baseline_plan"):
        path = local_path(plan.get(field))
        require((ROOT / path).is_file(), "Missing plan reference: " + path)
    local_path(plan.get("acceptance_root"))
    raw_tasks = plan.get("tasks")
    require(isinstance(raw_tasks, list) and raw_tasks, "Plan has no modules")
    tasks = {}
    owned, migrations, case_ids, case_files = {}, set(), set(), set()
    for task in raw_tasks:
        require(isinstance(task, dict), "Invalid module entry")
        task_id = task.get("id", "")
        require(isinstance(task_id, str) and re.fullmatch(r"W\d{2}", task_id), "Invalid module ID")
        require(task_id not in tasks, "Duplicate module ID: " + task_id)
        tasks[task_id] = task
        for field in ("title", "objective", "owns", "read_first", "steps", "start_command", "entry_path",
                      "e2e_command", "test_kind", "runtime_processes", "acceptance", "done_artifacts"):
            require(task.get(field), task_id + " missing " + field)
        for field in ("owns", "read_first", "steps", "runtime_processes", "acceptance", "done_artifacts", "depends_on"):
            require(isinstance(task.get(field), list), task_id + " requires array " + field)
        require(task.get("status") in STATUS_VALUES, "Invalid module status: " + task_id)
        require(type(task.get("commands_available_now")) is bool, "Missing command availability: " + task_id)
        require(task["status"] != "planned" or not task["commands_available_now"],
                "Planned module cannot claim available application commands: " + task_id)
        require(task["test_kind"] in CASE_KINDS - {"harness"}, "Invalid module test kind: " + task_id)
        require(task["entry_path"].startswith("/"), "Entry path must be absolute URL path: " + task_id)
        require(task["e2e_command"] == "make web-e2e MODULE=" + task_id, "Wrong E2E command: " + task_id)
        expected_start = f"make web-run MODULE={task_id} DATA_DIR=artifacts/manual/{task_id} LISTEN=127.0.0.1:0"
        require(task["start_command"] == expected_start, "Unsafe or inconsistent isolated startup: " + task_id)
        require(len(task["depends_on"]) == len(set(task["depends_on"])), "Duplicate dependency: " + task_id)
        for ref in task["read_first"]:
            path = local_path(ref)
            require((ROOT / path).is_file(), "Missing module reference: " + path)
        for raw in task["owns"]:
            path = local_path(raw)
            for previous, owner in owned.items():
                require(not (path == previous or path.startswith(previous + "/") or previous.startswith(path + "/")),
                        f"Overlapping ownership: {owner}:{previous} / {task_id}:{path}")
            owned[path] = task_id
        prefix = task.get("migration_prefix")
        if prefix is not None:
            require(type(prefix) is int and prefix >= 100 and prefix not in migrations, "Duplicate/invalid migration prefix")
            migrations.add(prefix)
            require(any(path.startswith(f"migrations/{prefix}_") for path in task["owns"]),
                    "Migration prefix has no owned migration file: " + task_id)
        for case in task["acceptance"]:
            require(isinstance(case, dict), "Invalid case entry: " + task_id)
            case_id = case.get("id", "")
            require(isinstance(case_id, str) and re.fullmatch(task_id + r"-\d{2}", case_id), "Invalid case ID: " + task_id)
            require(case_id not in case_ids, "Duplicate case ID: " + case_id)
            case_ids.add(case_id)
            require(case.get("kind") in CASE_KINDS and case.get("scenario"), "Case missing scenario/type: " + case_id)
            file_path = local_path(case.get("file"))
            require(re.fullmatch(r"e2e/cases/" + task_id + r"/test_\w+\.py", file_path), "Wrong case path: " + case_id)
            require(file_path not in case_files, "Case file registered twice: " + file_path)
            case_files.add(file_path)
            require(any(file_path == local_path(path) or file_path.startswith(local_path(path) + "/") for path in task["owns"]),
                    "Case is not owned by its module: " + case_id)
        require(any(case["kind"] != "harness" for case in task["acceptance"]), "No real business E2E case: " + task_id)
        if task["test_kind"] != "api":
            require(any(case["kind"] in {"browser", "browser_worker"} for case in task["acceptance"]),
                    "UI module has no browser E2E: " + task_id)

    require(plan.get("shared_owner") in tasks, "Shared owner is not registered")
    visited, visiting, order = set(), set(), []

    def visit(task_id):
        require(task_id in tasks, "Unknown dependency: " + str(task_id))
        require(task_id not in visiting, "Dependency cycle: " + task_id)
        if task_id in visited:
            return
        visiting.add(task_id)
        for dependency in tasks[task_id]["depends_on"]:
            visit(dependency)
        visiting.remove(task_id)
        visited.add(task_id)
        order.append(task_id)

    for task_id in tasks:
        visit(task_id)
    guide = (ROOT / plan["implementation_guide"]).read_text(encoding="utf-8")
    for task in tasks.values():
        require(re.search(r"(?m)^## " + task["id"] + r"\s+" + re.escape(task["title"]), guide),
                "Missing or mismatched human-readable module card: " + task["id"])
        for case in task["acceptance"]:
            require(case["id"] in guide, "Case missing from implementation guide: " + case["id"])
        check_completed(task, tasks, plan)
    return plan, tasks, order


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["check", "list", "show"])
    parser.add_argument("task_id", nargs="?")
    args = parser.parse_args()
    try:
        plan, tasks, order = load_plan()
        ready = [task_id for task_id in order if tasks[task_id]["status"] == "planned"
                 and all(tasks[dep]["status"] == "complete" for dep in tasks[task_id]["depends_on"])]
        if args.action != "show" and args.task_id:
            parser.error("task_id is only valid for show")
        if args.action == "check":
            print(json.dumps({"status": "ok", "design_version": plan["design_version"], "modules": len(tasks),
                              "required_cases": sum(len(task["acceptance"]) for task in tasks.values()),
                              "ready_to_implement": ready, "scope": "plan integrity only; no application or E2E executed"},
                             ensure_ascii=False))
        elif args.action == "list":
            for task_id in order:
                task = tasks[task_id]
                dependencies = ", ".join(task["depends_on"]) or "none"
                print(f'{task_id} [{task["status"]}] {task["title"]} | dependencies: {dependencies} | cases: {len(task["acceptance"])}')
            print("Application commands are delivery contracts until implemented. This command starts no service.")
        else:
            if args.task_id not in tasks:
                parser.error("show requires a registered module ID")
            print("Workspace: " + str(ROOT))
            print("Read the referenced design and contract first. Own only declared files; do not revert others' edits.")
            print("Shared changes belong to W00 and must be integrated before this module's acceptance.")
            print("Use real processes and isolated data. Zero, skipped, mocked application flows or missing evidence cannot pass.")
            print(json.dumps(tasks[args.task_id], indent=2, ensure_ascii=False))
    except (ValueError, OSError, KeyError, TypeError) as exc:
        print("Web plan error: " + str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
