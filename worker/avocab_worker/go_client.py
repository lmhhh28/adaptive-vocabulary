"""Go backend internal generation protocol HTTP client."""
import json
from typing import Any
import uuid
import httpx


class GoAPIError(Exception):
    def __init__(self, status_code: int, code: str, message: str, details: dict | None = None):
        super().__init__(f"[{status_code}] {code}: {message}")
        self.status_code = status_code
        self.code = code
        self.message = message
        self.details = details or {}


class LeaseLostError(GoAPIError):
    pass


class BudgetExhaustedError(GoAPIError):
    pass


class CallAttemptsExhaustedError(GoAPIError):
    pass


class IdempotencyConflictError(GoAPIError):
    pass


class CallSettlementConflictError(GoAPIError):
    pass


class MaintenanceActiveError(GoAPIError):
    pass


class InputVersionChangedError(GoAPIError):
    pass


def _map_error(resp: httpx.Response):
    try:
        data = resp.json()
        err_obj = data.get("error", {})
        code = err_obj.get("code", "UNKNOWN_ERROR")
        msg = err_obj.get("message", resp.text)
        details = err_obj.get("details", {})
    except Exception:
        code = "HTTP_ERROR"
        msg = resp.text
        details = {}

    if code == "LEASE_LOST":
        raise LeaseLostError(resp.status_code, code, msg, details)
    elif code == "BUDGET_EXHAUSTED":
        raise BudgetExhaustedError(resp.status_code, code, msg, details)
    elif code == "CALL_ATTEMPTS_EXHAUSTED":
        raise CallAttemptsExhaustedError(resp.status_code, code, msg, details)
    elif code == "IDEMPOTENCY_CONFLICT":
        raise IdempotencyConflictError(resp.status_code, code, msg, details)
    elif code == "CALL_SETTLEMENT_CONFLICT":
        raise CallSettlementConflictError(resp.status_code, code, msg, details)
    elif code == "INPUT_VERSION_CHANGED":
        raise InputVersionChangedError(resp.status_code, code, msg, details)
    elif code == "MAINTENANCE":
        raise MaintenanceActiveError(resp.status_code, code, msg, details)
    else:
        raise GoAPIError(resp.status_code, code, msg, details)


class GoGenerationClient:
    def __init__(self, base_url: str, worker_token: str, worker_id: str = "worker-langgraph-1"):
        self.base_url = base_url.rstrip("/")
        self.worker_token = worker_token
        self.worker_id = worker_id
        self._client = httpx.Client(
            base_url=self.base_url,
            timeout=15.0,
            headers={
                "Authorization": f"Bearer {worker_token}",
                "Content-Type": "application/json; charset=utf-8"
            }
        )

    def close(self):
        self._client.close()

    def claim(self, limit: int = 1, capabilities: list[str] | None = None) -> list[dict[str, Any]]:
        payload = {
            "worker_id": self.worker_id,
            "capabilities": capabilities or ["meaning_recognition", "reverse_selection", "context_selection"],
            "limit": limit
        }
        resp = self._client.post("/internal/v1/generation/claim", json=payload)
        if resp.status_code != 200:
            _map_error(resp)
        return resp.json().get("items", [])

    def renew(self, item_id: str, fencing_token: int, input_digest: str) -> int:
        payload = {
            "worker_id": self.worker_id,
            "item_id": item_id,
            "fencing_token": fencing_token,
            "input_digest": input_digest
        }
        resp = self._client.post("/internal/v1/generation/renew", json=payload)
        if resp.status_code != 200:
            _map_error(resp)
        return resp.json()["lease_until"]

    def reserve(
        self,
        item_id: str,
        fencing_token: int,
        input_digest: str,
        logical_call_key: dict[str, Any],
        network_attempt_id: str,
        profile_version: str = "v1",
        input_sha256: str = "mock-input-sha",
        input_token_estimate: int = 200,
        max_output_tokens: int = 512,
        idempotency_key: str | None = None
    ) -> dict[str, Any]:
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
        resp = self._client.post("/internal/v1/generation/calls/reserve", json=payload, headers=headers)
        if resp.status_code != 200:
            _map_error(resp)
        return resp.json()

    def settle(
        self,
        reservation_id: str,
        item_id: str,
        fencing_token: int,
        input_digest: str,
        outcome: str = "response",
        provider_request_id: str | None = None,
        response_sha256: str | None = None,
        usage: dict[str, int] | None = None
    ) -> dict[str, Any]:
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
        resp = self._client.post(f"/internal/v1/generation/calls/{reservation_id}/settle", json=payload)
        if resp.status_code != 200:
            _map_error(resp)
        return resp.json()

    def submit_candidate(
        self,
        item_id: str,
        fencing_token: int,
        input_digest: str,
        attempt_number: int,
        candidate_index: int,
        content: dict[str, Any],
        generator_reservation_id: str,
        evaluator_reservation_ids: list[str],
        checks: dict[str, Any],
        parent_candidate_id: str | None = None,
        idempotency_key: str | None = None
    ) -> dict[str, Any]:
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
        resp = self._client.post("/internal/v1/generation/candidates", json=payload, headers=headers)
        if resp.status_code != 200:
            _map_error(resp)
        return resp.json()

    def complete(
        self,
        item_id: str,
        fencing_token: int,
        input_digest: str,
        outcome: str = "completed",
        reason: str | None = None
    ) -> dict[str, Any]:
        payload = {
            "worker_id": self.worker_id,
            "item_id": item_id,
            "fencing_token": fencing_token,
            "input_digest": input_digest,
            "outcome": outcome,
            "reason": reason
        }
        resp = self._client.post("/internal/v1/generation/complete", json=payload)
        if resp.status_code != 200:
            _map_error(resp)
        return resp.json()
