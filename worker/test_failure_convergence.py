import sqlite3
from pathlib import Path
import unittest
from unittest.mock import MagicMock

from avocab_worker.config import WorkerConfig
from avocab_worker.go_client import BudgetExhaustedError, GoGenerationClient, LeaseLostError
from avocab_worker.graph import GenerationWorkflow, WorkerState
from avocab_worker.provider_client import ProviderClient, ProviderError
from langgraph.checkpoint.sqlite import SqliteSaver


class TestWorkerFailureConvergence(unittest.TestCase):
    def setUp(self):
        self.go_client = MagicMock(spec=GoGenerationClient)
        self.provider_client = MagicMock(spec=ProviderClient)
        profile = {
            "config_version": "v1",
            "budget_policy_version": "v1",
            "quality_policy_version": "v1",
            "generator": {"model": "gen-model", "max_output_tokens": 512},
            "evaluator": {"model": "eval-model", "max_output_tokens": 256},
        }
        self.config = WorkerConfig(
            data_dir=Path("/tmp"),
            server_url="http://test",
            worker_token="tok",
            profile=profile,
            worker_id="w1",
        )
        self.conn = sqlite3.connect(":memory:", check_same_thread=False)
        self.saver = SqliteSaver(self.conn)
        self.workflow = GenerationWorkflow(self.go_client, self.provider_client, self.config, self.saver)

    def tearDown(self):
        self.conn.close()

    def base_state(self) -> WorkerState:
        return {
            "item_id": "item-1",
            "job_id": "job-1",
            "fixed_input": {"required_task_families": ["meaning_recognition"]},
            "input_digest": "digest-1",
            "fencing_token": 100,
            "attempt_number": 1,
            "candidate_index": 0,
            "revision_count": 0,
            "max_revisions": 2,
            "current_feedback": None,
            "candidate_content": None,
            "parent_candidate_id": None,
            "generator_reservation_id": None,
            "evaluator_reservation_ids": [],
            "semantic_evaluations": [],
            "checks": None,
            "verdict": None,
            "disposition": None,
            "published_version_id": None,
            "status": "processing",
            "stop_reason": None,
            "lease_lost": False,
            "crash_after_gen_hook": False,
        }

    def test_budget_exhausted_on_generator_reserve(self):
        self.go_client.reserve.side_effect = BudgetExhaustedError(409, "BUDGET_EXHAUSTED", "out of tokens")
        state = self.base_state()
        result = self.workflow.graph.invoke(state, {"configurable": {"thread_id": "t1"}})
        self.assertEqual(result["status"], "budget_exhausted")
        self.assertFalse(result["lease_lost"])
        self.go_client.complete.assert_called_once_with(
            item_id="item-1",
            fencing_token=100,
            input_digest="digest-1",
            outcome="budget_exhausted",
            reason="[409] BUDGET_EXHAUSTED: out of tokens",
        )

    def test_provider_error_on_call_generator(self):
        self.go_client.reserve.return_value = {"reservation_id": "res-gen-1"}
        self.provider_client.call_chat_completion.side_effect = ProviderError("provider down 503")
        state = self.base_state()
        result = self.workflow.graph.invoke(state, {"configurable": {"thread_id": "t2"}})
        self.assertEqual(result["status"], "failed")
        self.assertFalse(result["lease_lost"])
        self.go_client.settle.assert_called_once_with(
            reservation_id="res-gen-1",
            item_id="item-1",
            fencing_token=100,
            input_digest="digest-1",
            outcome="network_error",
        )
        self.go_client.complete.assert_called_once_with(
            item_id="item-1",
            fencing_token=100,
            input_digest="digest-1",
            outcome="failed",
            reason="Provider call failed: provider down 503",
        )

    def test_lease_lost_on_generator_reserve_halts_without_complete(self):
        self.go_client.reserve.side_effect = LeaseLostError(409, "LEASE_LOST", "lease expired")
        state = self.base_state()
        result = self.workflow.graph.invoke(state, {"configurable": {"thread_id": "t3"}})
        self.assertEqual(result["status"], "failed")
        self.assertTrue(result["lease_lost"])
        self.go_client.complete.assert_not_called()

    def test_budget_exhausted_on_evaluator_reserve(self):
        self.go_client.reserve.side_effect = [
            {"reservation_id": "res-gen-1"},
            {"reservation_id": "eval-1"},
            BudgetExhaustedError(409, "BUDGET_EXHAUSTED", "eval budget limit"),
        ]
        mock_resp = MagicMock()
        mock_resp.content = {"task_family": "meaning_recognition", "stem": "hello", "options": []}
        mock_resp.provider_request_id = "req-1"
        mock_resp.response_sha256 = "sha-1"
        mock_resp.usage = {"input_tokens": 10, "output_tokens": 20}
        self.provider_client.call_chat_completion.return_value = mock_resp

        state = self.base_state()
        result = self.workflow.graph.invoke(state, {"configurable": {"thread_id": "t4"}})
        self.assertEqual(result["status"], "budget_exhausted")
        self.assertFalse(result["lease_lost"])
        self.go_client.settle.assert_any_call(
            reservation_id="eval-1",
            item_id="item-1",
            fencing_token=100,
            input_digest="digest-1",
            outcome="network_error",
        )
        self.go_client.complete.assert_called_once_with(
            item_id="item-1",
            fencing_token=100,
            input_digest="digest-1",
            outcome="budget_exhausted",
            reason="[409] BUDGET_EXHAUSTED: eval budget limit",
        )


if __name__ == "__main__":
    unittest.main()
