"""Inspect and validate implementation cards; never pretends to run an absent module."""
import argparse
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def load_plan():
    plan = json.loads((ROOT / "config/implementation-plan-v1.json").read_text())
    tasks = {task["id"]: task for task in plan["tasks"]}
    if len(tasks) != len(plan["tasks"]):
        raise ValueError("Duplicate task ID")
    visited, visiting = set(), set()

    def visit(task_id):
        if task_id not in tasks:
            raise ValueError("Unknown dependency: " + task_id)
        if task_id in visiting:
            raise ValueError("Cyclic dependency: " + task_id)
        if task_id in visited:
            return
        visiting.add(task_id)
        task = tasks[task_id]
        for field in ("objective", "owns", "steps", "start_command", "e2e_command", "test_kind", "acceptance", "done_artifacts"):
            if not task.get(field):
                raise ValueError(task_id + " missing " + field)
        if task["status"] not in {"planned", "in_progress", "complete"}:
            raise ValueError("Invalid status: " + task_id)
        if task["contract_version"] != plan["design_version"]:
            raise ValueError("Contract mismatch: " + task_id)
        for ref in task["read_first"]:
            if not (ROOT / ref).is_file():
                raise ValueError("Missing task reference: " + ref)
        for dep in task["depends_on"]:
            visit(dep)
        if task["status"] == "complete" and any(tasks[d]["status"] != "complete" for d in task["depends_on"]):
            raise ValueError("Completed task has incomplete dependency: " + task_id)
        visiting.remove(task_id)
        visited.add(task_id)

    for task_id in tasks:
        visit(task_id)
    owned = {}
    migrations = set()
    for task in tasks.values():
        for path in task["owns"]:
            for existing, owner in owned.items():
                if path == existing or path.startswith(existing.rstrip("/") + "/") or existing.startswith(path.rstrip("/") + "/"):
                    raise ValueError("Overlapping ownership: " + owner + " / " + task["id"])
            owned[path] = task["id"]
        prefix = task["migration_prefix"]
        if prefix is not None:
            if prefix in migrations:
                raise ValueError("Duplicate migration prefix")
            migrations.add(prefix)
    return plan, tasks


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=["check", "list", "show"])
    parser.add_argument("task_id", nargs="?")
    args = parser.parse_args()
    plan, tasks = load_plan()
    ready = [t["id"] for t in tasks.values() if t["status"] == "planned" and all(tasks[d]["status"] == "complete" for d in t["depends_on"])]
    if args.action == "check":
        print(json.dumps({"status": "ok", "design_version": plan["design_version"], "tasks": len(tasks), "ready_to_implement": ready, "scope": "plan integrity; no application modules executed"}))
    elif args.action == "list":
        for task in tasks.values():
            remaining = [d for d in task["depends_on"] if tasks[d]["status"] != "complete"]
            readiness = "ready to implement" if task["id"] in ready else "depends on " + ",".join(remaining)
            print(f'{task["id"]} [{task["status"]}; {readiness}] {task["title"]}')
        print("Application commands below task cards are contracts until implemented; this command starts no service.")
    else:
        if args.task_id not in tasks:
            parser.error("show requires a known task ID")
        task = tasks[args.task_id]
        print("Workspace: " + str(ROOT))
        print("Implement this card within the frozen contract. Do not revert others' edits or move shared atomic transactions across modules.")
        print("Acceptance must use the declared real processes and database; zero tests, mocks of the system under test, and skipped requirements cannot pass.")
        print(json.dumps(task, indent=2, ensure_ascii=False))


if __name__ == "__main__":
    main()
