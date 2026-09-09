"""QL-01: Benchmark Negative Interception and Positive Pass Acceptance Test.

Verifies that:
1. All known negative cases across 6 categories (>=5 per category, >=30 total) are intercepted (0 leakage).
2. All positive cases (>=15 total across 3 task families) pass within the false-rejection tolerance (<=20%).
3. Final determination authorizes automated publishing under this profile.
"""
import json
import os
from pathlib import Path
import time

ROOT = Path(__file__).resolve().parents[3]


def run(context: dict) -> dict:
    case_dir: Path = context["case_dir"]
    artifacts = []

    # Import benchmark runner
    sys_path_added = False
    worker_dir = str(ROOT / "worker")
    import sys
    if worker_dir not in sys.path:
        sys.path.insert(0, worker_dir)
        sys_path_added = True

    try:
        from avocab_worker.quality_benchmark import QualityBenchmark

        report_path = ROOT / "artifacts/quality-benchmark/report.json"
        report = None

        # Check if recent report exists (within last 30 minutes)
        if report_path.is_file():
            try:
                data = json.loads(report_path.read_text(encoding="utf-8"))
                mtime = report_path.stat().st_mtime
                if time.time() - mtime < 1800 and data.get("determination", {}).get("authorized_for_auto_publishing"):
                    report = data
            except Exception:
                report = None

        if report is None:
            # Run benchmark directly
            profile_path = os.environ.get("PROFILE", str(ROOT / "config/production-profile.json"))
            benchmark = QualityBenchmark(profile_path=profile_path, allow_paid=True)
            report = benchmark.run(max_workers=3)
            report_path.parent.mkdir(parents=True, exist_ok=True)
            report_path.write_text(json.dumps(report, indent=2, ensure_ascii=False), encoding="utf-8")

        # Copy report to case artifacts
        case_report_path = case_dir / "report.json"
        case_report_path.write_text(json.dumps(report, indent=2, ensure_ascii=False), encoding="utf-8")
        artifacts.append(str(case_report_path))

        # Assert QL-01 requirements
        metrics = report["metrics"]
        neg = metrics["negative_cases"]
        pos = metrics["positive_cases"]
        cats = metrics["category_breakdown"]
        det = report["determination"]

        # 1. Total negative cases >= 30
        assert neg["total"] >= 30, f"Expected at least 30 negative cases, got {neg['total']}"

        # 2. Category coverage: at least 6 categories, at least 5 cases each
        assert len(cats) >= 6, f"Expected at least 6 negative categories, got {len(cats)}"
        for cat_name, cat_stats in cats.items():
            assert cat_stats["total"] >= 5, f"Category {cat_name} has {cat_stats['total']} cases, expected >= 5"
            assert cat_stats["leaked"] == 0, f"Category {cat_name} had {cat_stats['leaked']} leaked negatives!"
            assert cat_stats["interception_rate"] == 1.0, f"Category {cat_name} interception rate {cat_stats['interception_rate']} < 1.0"

        # 3. Zero negative leakage overall
        assert neg["leaked"] == 0, f"Expected 0 negative leaks, got {neg['leaked']}"
        assert neg["interception_rate"] == 1.0, f"Expected 100% negative interception, got {neg['interception_rate']}"

        # 4. Positive cases >= 15
        assert pos["total"] >= 15, f"Expected at least 15 positive cases, got {pos['total']}"
        assert pos["false_rejection_rate"] <= 0.20, f"Positive false rejection rate {pos['false_rejection_rate']} exceeds 20% limit"

        # 5. Determination authorized
        assert det["authorized_for_auto_publishing"] is True, f"Determination not authorized: {det['reason']}"

        return {
            "status": "passed",
            "artifacts": artifacts,
            "details": {
                "negative_total": neg["total"],
                "negative_intercepted": neg["intercepted"],
                "negative_leaked": neg["leaked"],
                "interception_rate": neg["interception_rate"],
                "positive_total": pos["total"],
                "positive_passed": pos["passed"],
                "false_rejection_rate": pos["false_rejection_rate"],
                "decision": det["decision"],
                "categories": len(cats)
            }
        }

    finally:
        if sys_path_added and worker_dir in sys.path:
            sys.path.remove(worker_dir)
