"""CLI entrypoint for Avocab Worker."""
import argparse
import json
import logging
from pathlib import Path
import sys

from .config import WorkerConfig
from .lab import run_lab
from .runner import WorkerRunner

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")


def main():
    parser = argparse.ArgumentParser(description="Adaptive Vocabulary Worker CLI")
    subparsers = parser.add_subparsers(dest="command", required=True)

    # run command
    run_parser = subparsers.add_parser("run", help="Start the worker polling loop")
    run_parser.add_argument("--data-dir", default="data/local", help="Path to data directory")
    run_parser.add_argument("--server", default="http://127.0.0.1:8080", help="Go server URL")
    run_parser.add_argument("--profile", default=None, help="Path to generation profile JSON")
    run_parser.add_argument("--worker-id", default="worker-1", help="Worker identifier")
    run_parser.add_argument("--max-items", type=int, default=None, help="Max items to process before exit")
    run_parser.add_argument("--crash-after-gen", action="store_true", help="Crash worker after generator response for WK-02")

    # lab command
    lab_parser = subparsers.add_parser("lab", help="Run local generation lab test")
    lab_parser.add_argument("--data-dir", default="artifacts/manual/M07", help="Path to lab data directory")

    args = parser.parse_args()

    if args.command == "run":
        cfg = WorkerConfig.load(
            data_dir=args.data_dir,
            server_url=args.server,
            profile_path=args.profile,
            worker_id=args.worker_id
        )
        runner = WorkerRunner(cfg)
        try:
            runner.run_loop(max_iterations=args.max_items, crash_after_gen=args.crash_after_gen)
        finally:
            runner.close()
    elif args.command == "lab":
        res = run_lab(args.data_dir)
        print(json.dumps(res, indent=2, ensure_ascii=False, default=str))


if __name__ == "__main__":
    main()
