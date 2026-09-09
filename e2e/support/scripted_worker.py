"""Scripted HTTP worker client for E2E testing of the internal generation protocol."""
from pathlib import Path
import uuid
import httpx


class ScriptedWorker:
    def __init__(self, base_url: str, worker_token: str, worker_id: str = "worker-scripted"):
        self.base_url = base_url.rstrip("/")
        self.worker_token = worker_token
        self.worker_id = worker_id
        self.client = httpx.Client(
            base_url=self.base_url,
            timeout=10.0,
            headers={"Authorization": f"Bearer {worker_token}"}
        )

    def close(self):
        self.client.close()

    def claim(self, limit: int = 1, capabilities: list[str] | None = None) -> httpx.Response:
        payload = {
            "worker_id": self.worker_id,
            "capabilities": capabilities or ["meaning_recognition", "reverse_selection", "context_selection"],
            "limit": limit
        }
        return self.client.post("/internal/v1/generation/claim", json=payload)

    def renew(self, item_id: str, fencing_token: int, input_digest: str) -> httpx.Response:
        payload = {
            "worker_id": self.worker_id,
            "item_id": item_id,
            "fencing_token": fencing_token,
            "input_digest": input_digest
        }
        return self.client.post("/internal/v1/generation/renew", json=payload)

    def reserve(
        self,
        item_id: str,
        fencing_token: int,
        input_digest: str,
        logical_call_key: dict,
        network_attempt_id: str,
        profile_version: str = "v1",
        input_sha256: str = "mock-input-sha",
        input_token_estimate: int = 200,
        max_output_tokens: int = 200,
        idempotency_key: str | None = None
    ) -> httpx.Response:
        payload = {
            "worker_id": self.worker_id,
            "item_id": item_id,
            "fencing_token": fencing_token,
            "input_digest": input_digest,
            "logical_call_key": logical_call_key,
            "network_attempt_id": network_attempt_id,
            "profile_version": profile_version,
            "input_sha256": input_sha256,
            "input_token_estimate": input_token_estimate,
            "max_output_tokens": max_output_tokens
        }
        headers = {}
        if idempotency_key:
            headers["Idempotency-Key"] = idempotency_key
        return self.client.post("/internal/v1/generation/calls/reserve", json=payload, headers=headers)

    def settle(
        self,
        reservation_id: str,
        item_id: str,
        fencing_token: int,
        input_digest: str,
        outcome: str = "response",
        provider_request_id: str | None = None,
        response_sha256: str | None = None,
        usage: dict | None = None
    ) -> httpx.Response:
        payload = {
            "worker_id": self.worker_id,
            "item_id": item_id,
            "fencing_token": fencing_token,
            "input_digest": input_digest,
            "outcome": outcome,
            "provider_request_id": provider_request_id,
            "response_sha256": response_sha256,
            "usage": usage
        }
        return self.client.post(f"/internal/v1/generation/calls/{reservation_id}/settle", json=payload)

    def submit_candidate(
        self,
        item_id: str,
        fencing_token: int,
        input_digest: str,
        attempt_number: int,
        candidate_index: int,
        content: dict,
        generator_reservation_id: str,
        evaluator_reservation_ids: list[str],
        checks: dict,
        parent_candidate_id: str | None = None,
        idempotency_key: str | None = None
    ) -> httpx.Response:
        payload = {
            "worker_id": self.worker_id,
            "item_id": item_id,
            "fencing_token": fencing_token,
            "input_digest": input_digest,
            "attempt_number": attempt_number,
            "candidate_index": candidate_index,
            "parent_candidate_id": parent_candidate_id,
            "content": content,
            "generation_metadata": {
                "generator_reservation_id": generator_reservation_id,
                "evaluator_reservation_ids": evaluator_reservation_ids
            },
            "checks": checks
        }
        headers = {}
        if idempotency_key:
            headers["Idempotency-Key"] = idempotency_key
        return self.client.post("/internal/v1/generation/candidates", json=payload, headers=headers)

    def complete(
        self,
        item_id: str,
        fencing_token: int,
        input_digest: str,
        outcome: str = "completed",
        reason: str | None = None
    ) -> httpx.Response:
        payload = {
            "worker_id": self.worker_id,
            "item_id": item_id,
            "fencing_token": fencing_token,
            "input_digest": input_digest,
            "outcome": outcome,
            "reason": reason
        }
        return self.client.post("/internal/v1/generation/complete", json=payload)

    def evaluated_reservations(self, item: dict, candidate_index: int) -> tuple[str, list[str]]:
        """Create three distinct settled calls bound to this exact candidate."""
        reservations = []
        for stage, evaluation_index in [("generator", 0), ("evaluator", 0), ("evaluator", 1)]:
            logical_key = {"attempt_number": item["attempt_number"], "stage": stage,
                           "candidate_index": candidate_index, "evaluation_index": evaluation_index}
            response = self.reserve(item["item_id"], item["fencing_token"], item["input_digest"],
                                    logical_key, str(uuid.uuid4()))
            assert response.status_code == 200, response.text
            rid = response.json()["reservation_id"]
            settled = self.settle(rid, item["item_id"], item["fencing_token"], item["input_digest"],
                                  outcome="response", usage={"input_tokens": 50, "output_tokens": 50})
            assert settled.status_code == 200, settled.text
            reservations.append(rid)
        return reservations[0], reservations[1:]
