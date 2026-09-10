"""Main worker runner orchestrating claims, graph execution, and checkpoints."""
import logging
from pathlib import Path
import signal
import sys
import time
from typing import Any, Optional

from langgraph.checkpoint.sqlite import SqliteSaver

from .config import WorkerConfig
from .go_client import GoGenerationClient
from .graph import GenerationWorkflow, WorkerState, connect_checkpoint_db
from .lease_manager import LeaseManager
from .provider_client import ProviderClient

logger = logging.getLogger("avocab_worker")


class WorkerRunner:
    def __init__(self, config: WorkerConfig):
        self.config = config
        self.go_client = GoGenerationClient(
            base_url=config.server_url,
            worker_token=config.worker_token,
            worker_id=config.worker_id
        )

        gen_base_url = config.generator_cfg.get("base_url") or "http://127.0.0.1:9999"
        self.provider_client = ProviderClient(
            base_url=gen_base_url,
            api_key=config.generator_api_key,
            default_model=config.generator_cfg.get("model", "test-model")
        )
        self.generator_client = self.provider_client

        eval_base_url = config.evaluator_cfg.get("base_url") or gen_base_url
        self.evaluator_client = ProviderClient(
            base_url=eval_base_url,
            api_key=config.evaluator_api_key,
            default_model=config.evaluator_cfg.get("model", "test-evaluator")
        )

        db_path = config.data_dir / "langgraph-checkpoints.db"
        self.checkpoint_conn = connect_checkpoint_db(db_path)
        self.checkpointer = SqliteSaver(self.checkpoint_conn)

        self.workflow = GenerationWorkflow(
            go_client=self.go_client,
            provider_client=self.provider_client,
            config=config,
            checkpointer=self.checkpointer,
            evaluator_client=self.evaluator_client
        )
        self.lease_manager = LeaseManager(self.go_client, renew_interval_seconds=30.0)
        self._shutdown = False

    def close(self):
        self._shutdown = True
        self.lease_manager.close()
        self.evaluator_client.close()
        self.provider_client.close()
        self.go_client.close()
        try:
            self.checkpoint_conn.close()
        except Exception:
            pass

    def process_item(
        self,
        item: dict[str, Any],
        crash_after_gen: bool = False
    ) -> WorkerState:
        item_id = item["item_id"]
        fencing_token = item["fencing_token"]
        input_digest = item["input_digest"]

        initial_state: WorkerState = {
            "item_id": item_id,
            "job_id": item["job_id"],
            "fixed_input": item["fixed_input"],
            "input_digest": input_digest,
            "fencing_token": fencing_token,
            "attempt_number": item.get("attempt_number", 1),
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
            "crash_after_gen_hook": crash_after_gen
        }

        thread_config = {"configurable": {"thread_id": f"item_{item_id}"}}

        with self.lease_manager.track(item_id, fencing_token, input_digest):
            result = self.workflow.graph.invoke(initial_state, thread_config)
            return result

    def run_once(self, limit: int = 1, crash_after_gen: bool = False) -> list[WorkerState]:
        claimed_items = self.go_client.claim(limit=limit)
        results = []
        for item in claimed_items:
            res = self.process_item(item, crash_after_gen=crash_after_gen)
            results.append(res)
        return results

    def run_loop(self, max_iterations: Optional[int] = None, poll_interval: float = 2.0, crash_after_gen: bool = False):
        def _handle_signal(sig, frame):
            logger.info("Worker received shutdown signal")
            self._shutdown = True

        signal.signal(signal.SIGINT, _handle_signal)
        signal.signal(signal.SIGTERM, _handle_signal)

        iterations = 0
        logger.info(f"Worker {self.config.worker_id} started, polling {self.config.server_url}...")

        while not self._shutdown:
            if max_iterations is not None and iterations >= max_iterations:
                break
            iterations += 1

            try:
                results = self.run_once(limit=1, crash_after_gen=crash_after_gen)
                if not results:
                    time.sleep(poll_interval)
            except Exception as exc:
                if crash_after_gen and "Simulated worker crash" in str(exc):
                    raise
                logger.error(f"Error in worker iteration: {exc}", exc_info=True)
                time.sleep(poll_interval)
