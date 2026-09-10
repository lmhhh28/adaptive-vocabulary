import sqlite3
from pathlib import Path
import unittest
from unittest.mock import MagicMock, patch

from avocab_worker.config import WorkerConfig
from avocab_worker.go_client import GoGenerationClient
from avocab_worker.graph import GenerationWorkflow, WorkerState
from avocab_worker.provider_client import ProviderClient
from avocab_worker.runner import WorkerRunner
from langgraph.checkpoint.sqlite import SqliteSaver


class TestWorkerP2Fixes(unittest.TestCase):
    def setUp(self):
        self.profile = {
            "config_version": "v1",
            "budget_policy_version": "v1",
            "quality_policy_version": "v1",
            "mode": "fixture",
            "generator": {
                "base_url": "http://127.0.0.1:9001",
                "api_key": "gen-key-123",
                "model": "gen-model",
                "max_output_tokens": 512,
            },
            "evaluator": {
                "base_url": "http://127.0.0.1:9002",
                "api_key": "eval-key-456",
                "model": "eval-model",
                "max_output_tokens": 256,
            },
        }

    def test_runner_initializes_separate_generator_and_evaluator_clients(self):
        tmp_dir = Path("/tmp")
        token_file = tmp_dir / "worker.token"
        token_file.write_text("token-abc")
        try:
            cfg = WorkerConfig(
                data_dir=tmp_dir,
                server_url="http://127.0.0.1:8080",
                worker_token="token-abc",
                profile=self.profile,
            )
            with patch("avocab_worker.runner.connect_checkpoint_db") as mock_conn:
                mock_conn.return_value = sqlite3.connect(":memory:")
                runner = WorkerRunner(cfg)
                self.assertEqual(runner.provider_client.base_url, "http://127.0.0.1:9001")
                self.assertEqual(runner.provider_client.api_key, "gen-key-123")
                self.assertEqual(runner.evaluator_client.base_url, "http://127.0.0.1:9002")
                self.assertEqual(runner.evaluator_client.api_key, "eval-key-456")
                self.assertIsNot(runner.provider_client, runner.evaluator_client)
                runner.close()
        finally:
            if token_file.exists():
                token_file.unlink()

    def test_evaluator_rejects_on_multiple_correct_options(self):
        go_client = MagicMock(spec=GoGenerationClient)
        gen_client = MagicMock(spec=ProviderClient)
        eval_client = MagicMock(spec=ProviderClient)
        cfg = WorkerConfig(
            data_dir=Path("/tmp"),
            server_url="http://test",
            worker_token="tok",
            profile=self.profile,
        )
        conn = sqlite3.connect(":memory:", check_same_thread=False)
        saver = SqliteSaver(conn)
        workflow = GenerationWorkflow(
            go_client=go_client,
            provider_client=gen_client,
            config=cfg,
            checkpointer=saver,
            evaluator_client=eval_client,
        )

        mock_eval_resp = MagicMock()
        mock_eval_resp.content = {
            "ratings": {
                "target_meaning": "pass",
                "grammar": "pass",
                "naturalness": "pass",
                "distractor_validity": "pass",
                "unique_answer": "pass",
                "explanation_consistency": "pass",
            },
            "rationale": "Looks good semantically",
            "model": "eval-model",
            "prompt_version": "v1",
        }
        mock_eval_resp.provider_request_id = "req-1"
        mock_eval_resp.response_sha256 = "sha-1"
        mock_eval_resp.usage = {"input_tokens": 10, "output_tokens": 20}
        eval_client.call_chat_completion.return_value = mock_eval_resp

        state: WorkerState = {
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
            # Two correct options
            "candidate_content": {
                "task_family": "meaning_recognition",
                "stem": "valid stem",
                "options": [
                    {"id": "o1", "is_correct": True, "text": "a"},
                    {"id": "o2", "is_correct": True, "text": "b"},
                    {"id": "o3", "is_correct": False, "text": "c"},
                    {"id": "o4", "is_correct": False, "text": "d"},
                ],
            },
            "parent_candidate_id": None,
            "generator_reservation_id": "gen-1",
            "evaluator_reservation_ids": ["eval-1", "eval-2"],
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

        res = workflow._node_call_evaluators(state)
        self.assertEqual(res["verdict"], "reject")
        self.assertEqual(res["checks"]["result"], "reject")
        self.assertFalse(res["checks"]["rule_checks"]["single_correct"])
        self.assertIn("exactly one correct option", res["current_feedback"])
        conn.close()

    def test_json_non_dict_raises_provider_error(self):
        import httpx
        from avocab_worker.provider_client import ProviderError
        provider = ProviderClient("http://127.0.0.1:9999", "probe-key")
        provider._client.close()
        provider._client = httpx.Client(
            base_url="http://127.0.0.1:9999",
            transport=httpx.MockTransport(
                lambda req: httpx.Response(200, json={
                    "id": "req-1",
                    "choices": [{"message": {"content": "null"}}],
                    "usage": {"prompt_tokens": 5, "completion_tokens": 1}
                })
            )
        )
        try:
            with self.assertRaises(ProviderError) as ctx:
                provider.call_chat_completion([{"role": "user", "content": "hi"}], "model", 100)
            self.assertIn("non-dict JSON content", str(ctx.exception))
        finally:
            provider.close()

    def test_generator_retries_on_503(self):
        from avocab_worker.provider_client import ProviderResponseError
        go_client = MagicMock(spec=GoGenerationClient)
        go_client.reserve.return_value = {"reservation_id": "retry-res"}
        gen_client = MagicMock(spec=ProviderClient)
        gen_client.call_chat_completion.side_effect = ProviderResponseError(503, "Service Unavailable")
        cfg = WorkerConfig(
            data_dir=Path("/tmp"),
            server_url="http://test",
            worker_token="tok",
            profile={**self.profile, "max_network_attempts_per_call": 3},
        )
        conn = sqlite3.connect(":memory:", check_same_thread=False)
        workflow = GenerationWorkflow(
            go_client=go_client,
            provider_client=gen_client,
            config=cfg,
            checkpointer=SqliteSaver(conn),
        )
        state: WorkerState = {
            "item_id": "item-retry",
            "job_id": "job-retry",
            "fixed_input": {"required_task_families": ["meaning_recognition"], "word": "test"},
            "input_digest": "digest-1",
            "fencing_token": 100,
            "attempt_number": 1,
            "candidate_index": 0,
            "generator_reservation_id": "gen-res-1",
            "status": "processing",
        }
        res = workflow._node_call_generator(state)
        # Should attempt 3 times
        self.assertEqual(gen_client.call_chat_completion.call_count, 3)
        self.assertEqual(res["status"], "failed")
        conn.close()


if __name__ == "__main__":
    unittest.main()

