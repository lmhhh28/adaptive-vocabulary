"""LangGraph StateGraph workflow for vocabulary generation, dual evaluation, and publishing."""
import json
from pathlib import Path
import sqlite3
from typing import Any, Optional, TypedDict
import uuid

import os
os.environ["LANGGRAPH_STRICT_MSGPACK"] = "true"
os.environ["LANGSMITH_TRACING"] = "false"

from langgraph.checkpoint.sqlite import SqliteSaver
from langgraph.graph import END, START, StateGraph

from .config import WorkerConfig
from .go_client import (
    BudgetExhaustedError,
    GoAPIError,
    GoGenerationClient,
    LeaseLostError,
)
from .prompts import build_evaluator_prompt, build_generator_prompt
from .provider_client import ProviderClient, ProviderError


class WorkerState(TypedDict, total=False):
    item_id: str
    job_id: str
    fixed_input: dict[str, Any]
    input_digest: str
    fencing_token: int
    attempt_number: int
    candidate_index: int
    revision_count: int
    max_revisions: int
    current_feedback: Optional[str]
    candidate_content: Optional[dict[str, Any]]
    parent_candidate_id: Optional[str]
    generator_reservation_id: Optional[str]
    evaluator_reservation_ids: list[str]
    semantic_evaluations: list[dict[str, Any]]
    checks: Optional[dict[str, Any]]
    verdict: Optional[str]  # "pass" | "reject" | "uncertain"
    disposition: Optional[str]  # "published" | "auto_passed" | "rejected" | "uncertain"
    published_version_id: Optional[str]
    status: str  # "processing" | "completed" | "needs_attention" | "budget_exhausted" | "failed"
    stop_reason: Optional[str]
    crash_after_gen_hook: bool


class GenerationWorkflow:
    def __init__(
        self,
        go_client: GoGenerationClient,
        provider_client: ProviderClient,
        config: WorkerConfig,
        checkpointer: SqliteSaver
    ):
        self.go_client = go_client
        self.provider_client = provider_client
        self.config = config
        self.checkpointer = checkpointer
        self.graph = self._build_graph()

    def _build_graph(self):
        builder = StateGraph(WorkerState)

        builder.add_node("reserve_generator", self._node_reserve_generator)
        builder.add_node("call_generator", self._node_call_generator)
        builder.add_node("reserve_evaluators", self._node_reserve_evaluators)
        builder.add_node("call_evaluators", self._node_call_evaluators)
        builder.add_node("prepare_revision", self._node_prepare_revision)
        builder.add_node("submit_candidate", self._node_submit_candidate)
        builder.add_node("complete_item", self._node_complete_item)

        builder.add_edge(START, "reserve_generator")
        builder.add_edge("reserve_generator", "call_generator")
        builder.add_edge("call_generator", "reserve_evaluators")
        builder.add_edge("reserve_evaluators", "call_evaluators")

        # Conditional branch after evaluation
        builder.add_conditional_edges(
            "call_evaluators",
            self._route_after_evaluators,
            {
                "revise": "prepare_revision",
                "submit": "submit_candidate",
                "halt": END
            }
        )
        builder.add_edge("prepare_revision", "reserve_generator")
        builder.add_edge("submit_candidate", "complete_item")
        builder.add_edge("complete_item", END)

        return builder.compile(checkpointer=self.checkpointer)

    def _node_reserve_generator(self, state: WorkerState) -> dict[str, Any]:
        if state.get("status") in ("budget_exhausted", "failed"):
            return {}

        item_id = state["item_id"]
        fencing_token = state["fencing_token"]
        input_digest = state["input_digest"]
        attempt_number = state.get("attempt_number", 1)
        candidate_index = state.get("candidate_index", 0)

        logical_call_key = {
            "attempt_number": attempt_number,
            "stage": "generator",
            "candidate_index": candidate_index
        }
        network_attempt_id = str(uuid.uuid4())

        try:
            resp = self.go_client.reserve(
                item_id=item_id,
                fencing_token=fencing_token,
                input_digest=input_digest,
                logical_call_key=logical_call_key,
                network_attempt_id=network_attempt_id,
                profile_version=self.config.config_version,
                input_sha256=f"input-sha-{item_id}-{candidate_index}",
                input_token_estimate=150,
                max_output_tokens=self.config.generator_cfg.get("max_output_tokens", 512)
            )
            return {"generator_reservation_id": resp["reservation_id"]}
        except BudgetExhaustedError as exc:
            return {"status": "budget_exhausted", "stop_reason": str(exc)}
        except LeaseLostError as exc:
            return {"status": "failed", "stop_reason": str(exc)}

    def _node_call_generator(self, state: WorkerState) -> dict[str, Any]:
        if state.get("status") in ("budget_exhausted", "failed"):
            return {}

        res_id = state.get("generator_reservation_id")
        if not res_id:
            return {"status": "failed", "stop_reason": "Missing generator_reservation_id"}

        fixed_input = state["fixed_input"]
        task_families = fixed_input.get("required_task_families", ["meaning_recognition"])
        task_family = task_families[0] if task_families else "meaning_recognition"
        feedback = state.get("current_feedback")

        messages = build_generator_prompt(fixed_input, task_family, feedback)

        try:
            call_res = self.provider_client.call_chat_completion(
                messages=messages,
                model=self.config.generator_cfg.get("model"),
                max_tokens=self.config.generator_cfg.get("max_output_tokens", 512)
            )
        except ProviderError as exc:
            # Settle as network_error
            try:
                self.go_client.settle(
                    reservation_id=res_id,
                    item_id=state["item_id"],
                    fencing_token=state["fencing_token"],
                    input_digest=state["input_digest"],
                    outcome="network_error"
                )
            except Exception:
                pass
            return {"status": "failed", "stop_reason": f"Provider call failed: {exc}"}

        # Settle success with Go
        try:
            self.go_client.settle(
                reservation_id=res_id,
                item_id=state["item_id"],
                fencing_token=state["fencing_token"],
                input_digest=state["input_digest"],
                outcome="response",
                provider_request_id=call_res.provider_request_id,
                response_sha256=call_res.response_sha256,
                usage=call_res.usage
            )
        except (BudgetExhaustedError, LeaseLostError) as exc:
            return {"status": "failed", "stop_reason": str(exc)}

        # Simulated crash hook for WK-02
        if state.get("crash_after_gen_hook"):
            raise RuntimeError("Simulated worker crash after generator response")

        candidate_content = call_res.content
        # Ensure task_family is present
        if "task_family" not in candidate_content:
            candidate_content["task_family"] = task_family

        return {"candidate_content": candidate_content}

    def _node_reserve_evaluators(self, state: WorkerState) -> dict[str, Any]:
        if state.get("status") in ("budget_exhausted", "failed"):
            return {}

        item_id = state["item_id"]
        fencing_token = state["fencing_token"]
        input_digest = state["input_digest"]
        attempt_number = state.get("attempt_number", 1)
        candidate_index = state.get("candidate_index", 0)

        reservation_ids = []
        for eval_idx in (0, 1):
            logical_call_key = {
                "attempt_number": attempt_number,
                "stage": "evaluator",
                "candidate_index": candidate_index,
                "evaluation_index": eval_idx
            }
            network_attempt_id = str(uuid.uuid4())
            try:
                resp = self.go_client.reserve(
                    item_id=item_id,
                    fencing_token=fencing_token,
                    input_digest=input_digest,
                    logical_call_key=logical_call_key,
                    network_attempt_id=network_attempt_id,
                    profile_version=self.config.config_version,
                    input_sha256=f"eval-sha-{item_id}-{candidate_index}-{eval_idx}",
                    input_token_estimate=120,
                    max_output_tokens=self.config.evaluator_cfg.get("max_output_tokens", 256)
                )
                reservation_ids.append(resp["reservation_id"])
            except BudgetExhaustedError as exc:
                return {"status": "budget_exhausted", "stop_reason": str(exc), "evaluator_reservation_ids": reservation_ids}
            except LeaseLostError as exc:
                return {"status": "failed", "stop_reason": str(exc), "evaluator_reservation_ids": reservation_ids}

        return {"evaluator_reservation_ids": reservation_ids}

    def _node_call_evaluators(self, state: WorkerState) -> dict[str, Any]:
        if state.get("status") in ("budget_exhausted", "failed"):
            return {}

        res_ids = state.get("evaluator_reservation_ids", [])
        if len(res_ids) < 2:
            return {"status": "failed", "stop_reason": "Insufficient evaluator reservations"}

        candidate_content = state.get("candidate_content", {})
        fixed_input = state["fixed_input"]

        evaluations = []
        for eval_idx, res_id in enumerate(res_ids[:2]):
            messages = build_evaluator_prompt(fixed_input, candidate_content, eval_idx)
            try:
                call_res = self.provider_client.call_chat_completion(
                    messages=messages,
                    model=self.config.evaluator_cfg.get("model"),
                    max_tokens=self.config.evaluator_cfg.get("max_output_tokens", 256)
                )
            except ProviderError as exc:
                try:
                    self.go_client.settle(
                        reservation_id=res_id,
                        item_id=state["item_id"],
                        fencing_token=state["fencing_token"],
                        input_digest=state["input_digest"],
                        outcome="network_error"
                    )
                except Exception:
                    pass
                return {"status": "failed", "stop_reason": f"Evaluator call failed: {exc}"}

            try:
                self.go_client.settle(
                    reservation_id=res_id,
                    item_id=state["item_id"],
                    fencing_token=state["fencing_token"],
                    input_digest=state["input_digest"],
                    outcome="response",
                    provider_request_id=call_res.provider_request_id,
                    response_sha256=call_res.response_sha256,
                    usage=call_res.usage
                )
            except (BudgetExhaustedError, LeaseLostError) as exc:
                return {"status": "failed", "stop_reason": str(exc)}

            eval_dict = {
                "profile_version": call_res.content.get("profile_version", "v1"),
                "model": call_res.content.get("model", self.config.evaluator_cfg.get("model", "test-evaluator")),
                "prompt_version": call_res.content.get("prompt_version", "v1"),
                "ratings": call_res.content.get("ratings", {}),
                "rationale": call_res.content.get("rationale", "")
            }
            evaluations.append(eval_dict)

        # Analyze evaluations
        eval_verdicts = []
        reasons = []
        for ev in evaluations:
            ratings = ev.get("ratings", {})
            if any(r == "uncertain" for r in ratings.values()):
                eval_verdicts.append("uncertain")
                reasons.append(ev.get("rationale", "Uncertain evaluation"))
            elif any(r == "reject" for r in ratings.values()):
                eval_verdicts.append("reject")
                reasons.append(ev.get("rationale", "Rejected evaluation"))
            elif all(r == "pass" for r in ratings.values()):
                eval_verdicts.append("pass")
            else:
                eval_verdicts.append("uncertain")

        if any(v == "uncertain" for v in eval_verdicts):
            verdict = "uncertain"
        elif "pass" in eval_verdicts and "reject" in eval_verdicts:
            # Conflict between evaluators -> uncertain
            verdict = "uncertain"
        elif all(v == "pass" for v in eval_verdicts):
            verdict = "pass"
        elif all(v == "reject" for v in eval_verdicts):
            verdict = "reject"
        else:
            verdict = "uncertain"

        checks = {
            "rule_checks": {
                "options_count": len(candidate_content.get("options", [])),
                "single_correct": sum(1 for o in candidate_content.get("options", []) if o.get("is_correct")) == 1,
                "stem_valid": bool(candidate_content.get("stem"))
            },
            "semantic_evaluations": evaluations,
            "result": verdict
        }

        return {
            "semantic_evaluations": evaluations,
            "checks": checks,
            "verdict": verdict,
            "current_feedback": " ; ".join(reasons) if reasons else None
        }

    def _route_after_evaluators(self, state: WorkerState) -> str:
        if state.get("status") in ("budget_exhausted", "failed"):
            return "halt"

        verdict = state.get("verdict", "pass")
        rev_count = state.get("revision_count", 0)
        max_rev = state.get("max_revisions", 2)

        if verdict == "reject" and rev_count < max_rev:
            return "revise"
        return "submit"

    def _node_prepare_revision(self, state: WorkerState) -> dict[str, Any]:
        return {
            "revision_count": state.get("revision_count", 0) + 1,
            "candidate_index": state.get("candidate_index", 0) + 1,
            "parent_candidate_id": state.get("parent_candidate_id"),
            "generator_reservation_id": None,
            "evaluator_reservation_ids": [],
            "semantic_evaluations": []
        }

    def _node_submit_candidate(self, state: WorkerState) -> dict[str, Any]:
        if state.get("status") in ("budget_exhausted", "failed"):
            return {}

        item_id = state["item_id"]
        fencing_token = state["fencing_token"]
        input_digest = state["input_digest"]
        attempt_number = state.get("attempt_number", 1)
        candidate_index = state.get("candidate_index", 0)
        content = state["candidate_content"] or {}
        gen_res_id = state["generator_reservation_id"] or ""
        eval_res_ids = state.get("evaluator_reservation_ids", [])
        checks = state.get("checks", {})
        parent_id = state.get("parent_candidate_id")

        try:
            resp = self.go_client.submit_candidate(
                item_id=item_id,
                fencing_token=fencing_token,
                input_digest=input_digest,
                attempt_number=attempt_number,
                candidate_index=candidate_index,
                content=content,
                generator_reservation_id=gen_res_id,
                evaluator_reservation_ids=eval_res_ids,
                checks=checks,
                parent_candidate_id=parent_id,
                idempotency_key=str(uuid.uuid4())
            )
            return {
                "disposition": resp.get("disposition"),
                "published_version_id": resp.get("published_version_id")
            }
        except (LeaseLostError, BudgetExhaustedError, GoAPIError) as exc:
            return {"status": "failed", "stop_reason": str(exc)}

    def _node_complete_item(self, state: WorkerState) -> dict[str, Any]:
        if state.get("status") in ("budget_exhausted", "failed"):
            return {}

        disposition = state.get("disposition")
        if disposition in ("published", "auto_passed"):
            outcome = "completed"
        elif state.get("status") == "budget_exhausted":
            outcome = "budget_exhausted"
        else:
            outcome = "needs_attention"

        item_id = state["item_id"]
        fencing_token = state["fencing_token"]
        input_digest = state["input_digest"]

        try:
            self.go_client.complete(
                item_id=item_id,
                fencing_token=fencing_token,
                input_digest=input_digest,
                outcome=outcome,
                reason=state.get("stop_reason")
            )
            return {"status": outcome}
        except Exception as exc:
            return {"status": "failed", "stop_reason": str(exc)}


def connect_checkpoint_db(path: Path) -> sqlite3.Connection:
    conn = sqlite3.connect(str(path), check_same_thread=False)
    conn.execute("PRAGMA foreign_keys=ON")
    conn.execute("PRAGMA busy_timeout=5000")
    conn.execute("PRAGMA synchronous=FULL")
    conn.execute("PRAGMA journal_mode=WAL")
    return conn
