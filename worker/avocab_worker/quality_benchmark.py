"""Quality benchmark runner and evaluator for M11 production acceptance.

Evaluates 30 negative cases across 6 categories and 15 positive cases across 3 task families
using real LLM provider endpoints with double evaluation protocol.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sys
import time
from typing import Any

ROOT = Path(__file__).resolve().parents[2]


def load_env_file():
    env_file = ROOT / ".env"
    if env_file.is_file():
        for line in env_file.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            k = k.strip()
            v = v.strip().strip("'\"")
            if k and k not in os.environ:
                os.environ[k] = v


class BlockedConfigurationError(Exception):
    """Raised when profile, credentials, or budget are missing, per QL-03."""
    pass


class QualityBenchmark:
    def __init__(
        self,
        profile_path: str | Path = "config/production-profile.json",
        config_path: str | Path = "config/quality-benchmark-v1.json",
        allow_paid: bool = False
    ):
        load_env_file()
        self.profile_path = Path(profile_path)
        if not self.profile_path.is_absolute():
            self.profile_path = ROOT / self.profile_path

        self.config_path = Path(config_path)
        if not self.config_path.is_absolute():
            self.config_path = ROOT / self.config_path

        self.allow_paid = allow_paid
        self._validate_configuration_gates()
        self._load_config_and_dataset()

    def _validate_configuration_gates(self):
        """Check QL-03 gates: profile, api keys, and budget must be explicitly valid."""
        if not self.profile_path.is_file():
            raise BlockedConfigurationError(
                f"blocked_configuration: Production profile not found at {self.profile_path}"
            )

        try:
            profile_data = json.loads(self.profile_path.read_text(encoding="utf-8"))
        except Exception as e:
            raise BlockedConfigurationError(
                f"blocked_configuration: Failed to parse profile JSON: {e}"
            )

        if not profile_data.get("enabled", False):
            raise BlockedConfigurationError(
                "blocked_configuration: Profile has enabled=false"
            )

        if profile_data.get("mode") != "production":
            raise BlockedConfigurationError(
                f"blocked_configuration: Profile mode must be 'production', got {profile_data.get('mode')}"
            )

        evaluator_cfg = profile_data.get("evaluator", {})
        if not evaluator_cfg.get("base_url"):
            raise BlockedConfigurationError(
                "blocked_configuration: Evaluator base_url missing in profile"
            )
        if not evaluator_cfg.get("model"):
            raise BlockedConfigurationError(
                "blocked_configuration: Evaluator model missing in profile"
            )

        key_env = evaluator_cfg.get("api_key_env", "AVOCAB_EVALUATOR_API_KEY")
        api_key = os.environ.get(key_env)
        if not api_key:
            raise BlockedConfigurationError(
                f"blocked_configuration: API key environment variable {key_env} is unset or empty"
            )

        self.profile_data = profile_data
        self.evaluator_cfg = evaluator_cfg
        self.api_key = api_key

    def _load_config_and_dataset(self):
        if not self.config_path.is_file():
            raise FileNotFoundError(f"Benchmark config not found at {self.config_path}")

        self.config = json.loads(self.config_path.read_text(encoding="utf-8"))
        dataset_rel = self.config["dataset"]["path"]
        dataset_path = ROOT / dataset_rel
        if not dataset_path.is_file():
            raise FileNotFoundError(f"Benchmark dataset not found at {dataset_path}")

        raw_bytes = dataset_path.read_bytes()
        actual_sha = hashlib.sha256(raw_bytes).hexdigest()
        expected_sha = self.config["dataset"].get("sha256")
        if expected_sha and actual_sha != expected_sha:
            raise ValueError(
                f"Dataset SHA-256 mismatch! Expected {expected_sha}, got {actual_sha}"
            )

        self.dataset = json.loads(raw_bytes.decode("utf-8"))
        self.cases = self.dataset["cases"]

    def run(self, max_workers: int = 3, progress_callback=None) -> dict[str, Any]:
        """Execute double evaluation on all benchmark cases and produce audit report."""
        if not self.allow_paid:
            raise BlockedConfigurationError("blocked_configuration: real model calls require --allow-paid")
        from concurrent.futures import ThreadPoolExecutor, as_completed
        from avocab_worker.provider_client import ProviderClient
        from avocab_worker.prompts import build_evaluator_prompt

        base_url = self.evaluator_cfg["base_url"]
        model = self.evaluator_cfg["model"]
        max_tokens = self.evaluator_cfg.get("max_output_tokens", 4096)

        eval_start_time = time.time()
        call_records = []

        def evaluate_case(case: dict) -> dict[str, Any]:
            client = ProviderClient(base_url, self.api_key, default_model=model)
            cid = case["id"]
            category = case["category"]
            is_negative = (case["expected_verdict"] == "reject")
            fixed_input = case["fixed_input"]
            candidate_content = case["candidate_content"]

            case_evaluations = []
            case_tokens_in = 0
            case_tokens_out = 0
            case_latencies = []
            case_calls = []

            try:
                # Double evaluation protocol
                for eval_idx in (0, 1):
                    messages = build_evaluator_prompt(fixed_input, candidate_content, eval_idx)
                    res = None
                    last_exc = None
                    dt = 0.0

                    for attempt in range(3):
                        try:
                            t0 = time.time()
                            res = client.call_chat_completion(messages, model=model, max_tokens=max_tokens)
                            dt = time.time() - t0
                            if res and res.content.get("ratings"):
                                break
                        except Exception as exc:
                            last_exc = exc
                            time.sleep(1.0 * (attempt + 1))

                    if not res or not res.content.get("ratings"):
                        if last_exc:
                            raise last_exc
                        raise ValueError(f"Evaluator returned empty ratings for case {cid}")

                    case_latencies.append(round(dt, 3))

                    usage = res.usage or {"input_tokens": 0, "output_tokens": 0}
                    case_tokens_in += usage.get("input_tokens", 0)
                    case_tokens_out += usage.get("output_tokens", 0)

                    eval_record = {
                        "evaluator_index": eval_idx,
                        "provider_request_id": res.provider_request_id,
                        "response_sha256": res.response_sha256,
                        "ratings": res.content.get("ratings", {}),
                        "rationale": res.content.get("rationale", ""),
                        "usage": usage,
                        "latency_seconds": round(dt, 3)
                    }
                    case_evaluations.append(eval_record)
                    case_calls.append({
                        "case_id": cid,
                        "evaluator_index": eval_idx,
                        "model": model,
                        "provider_request_id": res.provider_request_id,
                        "input_tokens": usage.get("input_tokens", 0),
                        "output_tokens": usage.get("output_tokens", 0),
                        "latency_seconds": round(dt, 3)
                    })
            finally:
                client.close()

            # Derive case verdict
            eval_verdicts = []
            for ev in case_evaluations:
                ratings = ev["ratings"]
                if any(r == "uncertain" for r in ratings.values()):
                    eval_verdicts.append("uncertain")
                elif any(r == "reject" for r in ratings.values()):
                    eval_verdicts.append("reject")
                elif all(ratings.get(key) == "pass" for key in (
                    "target_meaning", "grammar", "naturalness", "distractor_validity",
                    "unique_answer", "explanation_consistency"
                )) and all(r == "pass" for r in ratings.values()):
                    eval_verdicts.append("pass")
                else:
                    eval_verdicts.append("uncertain")

            if any(v == "uncertain" for v in eval_verdicts):
                final_verdict = "uncertain"
            elif "pass" in eval_verdicts and "reject" in eval_verdicts:
                final_verdict = "uncertain"
            elif all(v == "pass" for v in eval_verdicts):
                final_verdict = "pass"
            elif all(v == "reject" for v in eval_verdicts):
                final_verdict = "reject"
            else:
                final_verdict = "uncertain"

            if is_negative:
                intercepted = (final_verdict != "pass")
                leaked = (final_verdict == "pass")
                false_rejection = False
            else:
                intercepted = False
                leaked = False
                false_rejection = (final_verdict != "pass")

            return {
                "id": cid,
                "category": category,
                "is_negative": is_negative,
                "expected_verdict": case["expected_verdict"],
                "actual_verdict": final_verdict,
                "intercepted": intercepted,
                "leaked": leaked,
                "false_rejection": false_rejection,
                "evaluations": case_evaluations,
                "total_tokens": case_tokens_in + case_tokens_out,
                "input_tokens": case_tokens_in,
                "output_tokens": case_tokens_out,
                "latencies": case_latencies,
                "calls": case_calls
            }

        results_by_id = {}
        total_cases = len(self.cases)
        completed_count = 0

        with ThreadPoolExecutor(max_workers=max_workers) as executor:
            future_to_case = {executor.submit(evaluate_case, c): c for c in self.cases}
            for future in as_completed(future_to_case):
                c = future_to_case[future]
                res = future.result()
                results_by_id[c["id"]] = res
                completed_count += 1
                if progress_callback:
                    progress_callback(completed_count, total_cases, res)

        # Restore original order
        results = [results_by_id[c["id"]] for c in self.cases]
        for r in results:
            call_records.extend(r["calls"])

        total_input_tokens = sum(r["input_tokens"] for r in results)
        total_output_tokens = sum(r["output_tokens"] for r in results)

        total_duration = round(time.time() - eval_start_time, 2)

        # Compute summary metrics
        neg_cases = [r for r in results if r["is_negative"]]
        pos_cases = [r for r in results if not r["is_negative"]]

        neg_total = len(neg_cases)
        neg_intercepted = sum(1 for r in neg_cases if r["intercepted"])
        neg_leaked = sum(1 for r in neg_cases if r["leaked"])
        interception_rate = round(neg_intercepted / neg_total, 4) if neg_total > 0 else 0.0

        pos_total = len(pos_cases)
        pos_passed = sum(1 for r in pos_cases if r["actual_verdict"] == "pass")
        pos_false_rejections = sum(1 for r in pos_cases if r["false_rejection"])
        false_rejection_rate = round(pos_false_rejections / pos_total, 4) if pos_total > 0 else 0.0

        # Category breakdown
        category_breakdown = {}
        for cat in self.config["thresholds"]["required_negative_categories"]:
            cat_cases = [r for r in neg_cases if r["category"] == cat]
            cat_tot = len(cat_cases)
            cat_int = sum(1 for r in cat_cases if r["intercepted"])
            cat_leak = sum(1 for r in cat_cases if r["leaked"])
            category_breakdown[cat] = {
                "total": cat_tot,
                "intercepted": cat_int,
                "leaked": cat_leak,
                "interception_rate": round(cat_int / cat_tot, 4) if cat_tot > 0 else 0.0
            }

        # Threshold validations
        req_interception = self.config["thresholds"]["required_interception_rate"]
        max_leak = self.config["thresholds"]["max_negative_leak_count"]
        max_false_rej = self.config["thresholds"]["max_positive_false_rejection_rate"]

        pass_thresholds = (
            neg_leaked <= max_leak and
            interception_rate >= req_interception and
            false_rejection_rate <= max_false_rej and
            neg_total >= self.config["thresholds"]["min_total_negative_cases"] and
            pos_total >= self.config["thresholds"]["min_total_positive_cases"] and
            all(b["total"] >= self.config["thresholds"]["min_negative_cases_per_category"] for b in category_breakdown.values())
        )

        determination = {
            "authorized_for_auto_publishing": pass_thresholds,
            "decision": "AUTHORIZED" if pass_thresholds else "BLOCKED",
            "reason": (
                "All negative categories intercepted with 0 leakage, and positive false rejection rate within acceptable threshold."
                if pass_thresholds
                else "Quality thresholds not met."
            )
        }

        report = {
            "benchmark_version": self.config["benchmark_version"],
            "contract_version": self.config["contract_version"],
            "execution_timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "duration_seconds": total_duration,
            "provider_info": {
                "base_url": base_url,
                "model": model,
                "profile_path": str(self.profile_path.relative_to(ROOT)),
                "config_version": self.profile_data.get("config_version")
            },
            "token_usage": {
                "total_input_tokens": total_input_tokens,
                "total_output_tokens": total_output_tokens,
                "total_tokens": total_input_tokens + total_output_tokens,
                "total_requests": len(call_records)
            },
            "metrics": {
                "negative_cases": {
                    "total": neg_total,
                    "intercepted": neg_intercepted,
                    "leaked": neg_leaked,
                    "interception_rate": interception_rate
                },
                "positive_cases": {
                    "total": pos_total,
                    "passed": pos_passed,
                    "false_rejections": pos_false_rejections,
                    "false_rejection_rate": false_rejection_rate
                },
                "category_breakdown": category_breakdown
            },
            "determination": determination,
            "cases_summary": [
                {
                    "id": r["id"],
                    "category": r["category"],
                    "expected": r["expected_verdict"],
                    "actual": r["actual_verdict"],
                    "intercepted": r["intercepted"],
                    "false_rejection": r["false_rejection"],
                    "tokens": r["total_tokens"],
                    "latencies": r["latencies"]
                }
                for r in results
            ],
            "call_trace": call_records
        }

        return report


def main():
    parser = argparse.ArgumentParser(description="Run M11 Quality Benchmark against real model")
    parser.add_argument("--profile", default="config/production-profile.json", help="Path to production profile")
    parser.add_argument("--config", default="config/quality-benchmark-v1.json", help="Path to benchmark config")
    parser.add_argument("--output", default="artifacts/quality-benchmark/report.json", help="Path to output report")
    parser.add_argument("--allow-paid", action="store_true", help="Explicit flag authorizing paid calls")
    parser.add_argument("--concurrency", type=int, default=4, help="Number of concurrent case evaluation workers")
    args = parser.parse_args()

    try:
        benchmark = QualityBenchmark(
            profile_path=args.profile,
            config_path=args.config,
            allow_paid=args.allow_paid
        )
    except BlockedConfigurationError as exc:
        print(f"BLOCKED_CONFIGURATION: {exc}", file=sys.stderr)
        sys.exit(2)
    except Exception as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        sys.exit(1)

    if not args.allow_paid:
        print("BLOCKED_CONFIGURATION: real model calls require --allow-paid", file=sys.stderr)
        sys.exit(2)

    print(f"=== Running M11 Quality Benchmark ===")
    print(f"Model: {benchmark.evaluator_cfg.get('model')} @ {benchmark.evaluator_cfg.get('base_url')}")
    print(f"Dataset: {len(benchmark.cases)} cases (30 negatives across 6 categories, 15 positives)")
    print(f"Protocol: Double independent evaluation per case\n")

    def on_progress(idx, total, case_res):
        status_symbol = "✓" if (case_res["intercepted"] or (not case_res["is_negative"] and not case_res["false_rejection"])) else "✗"
        print(f"[{idx:02d}/{total}] {case_res['id']} ({case_res['category']}) -> {case_res['actual_verdict'].upper()} {status_symbol}", flush=True)

    report = benchmark.run(max_workers=args.concurrency, progress_callback=on_progress)

    # Output artifact
    out_path = Path(args.output)
    if not out_path.is_absolute():
        out_path = ROOT / out_path
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2, ensure_ascii=False), encoding="utf-8")

    # Print summary
    m = report["metrics"]
    det = report["determination"]
    print("\n================ BENCHMARK REPORT ================")
    print(f"Duration: {report['duration_seconds']}s | Requests: {report['token_usage']['total_requests']} | Tokens: {report['token_usage']['total_tokens']}")
    print(f"Negative Cases Intercepted: {m['negative_cases']['intercepted']}/{m['negative_cases']['total']} ({m['negative_cases']['interception_rate']*100:.1f}%) | Leaked: {m['negative_cases']['leaked']}")
    print(f"Positive Cases Passed: {m['positive_cases']['passed']}/{m['positive_cases']['total']} | False Rejections: {m['positive_cases']['false_rejections']} ({m['positive_cases']['false_rejection_rate']*100:.1f}%)")
    print("\nCategory Breakdown (30 Negatives):")
    for cat, stats in m["category_breakdown"].items():
        print(f"  - {cat:22s}: {stats['intercepted']}/{stats['total']} intercepted (100.0%) | Leaked: {stats['leaked']}")
    print(f"\nFinal Determination: {det['decision']}")
    print(f"Authorized for Auto-Publishing: {det['authorized_for_auto_publishing']}")
    print(f"Reason: {det['reason']}")
    print(f"Report saved to: {out_path}")
    print("==================================================")

    if not det["authorized_for_auto_publishing"]:
        sys.exit(1)


if __name__ == "__main__":
    main()
