"""Exercise the actual provider parser and graph using a local HTTP mock."""
import json
from pathlib import Path
import sqlite3
import sys
from unittest.mock import MagicMock
import httpx

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / 'worker'))
from avocab_worker.config import WorkerConfig
from avocab_worker.go_client import GoGenerationClient
from avocab_worker.provider_client import ProviderClient, ProviderResponseError
from avocab_worker.graph import GenerationWorkflow
from langgraph.checkpoint.sqlite import SqliteSaver

go = MagicMock(spec=GoGenerationClient)
go.reserve.return_value = {'reservation_id': 'probe-reservation'}
provider = ProviderClient('http://127.0.0.1:9999', 'probe-key')
provider._client.close()
provider._client = httpx.Client(base_url='http://127.0.0.1:9999', transport=httpx.MockTransport(
    lambda request: httpx.Response(200, json={'id': 'local-request', 'choices': [{'message': {'content': 'null'}}], 'usage': {'prompt_tokens': 10, 'completion_tokens': 1}})))
config = WorkerConfig(Path('/tmp'), 'http://127.0.0.1:1', 'probe', {'config_version': 'generation-v2', 'generator': {}, 'evaluator': {}})
conn = sqlite3.connect(':memory:', check_same_thread=False)
workflow = GenerationWorkflow(go, provider, config, SqliteSaver(conn))
try:
    workflow.graph.invoke({'item_id': 'item-probe', 'job_id': 'job-probe', 'fencing_token': 1, 'input_digest': 'digest', 'fixed_input': {'required_task_families': ['meaning_recognition']}, 'status': 'processing'}, {'configurable': {'thread_id': 'probe'}})
    raise AssertionError('Expected parser shape failure')
except TypeError as exc:
    result = {'exception': str(exc), 'settlement_calls': go.settle.call_count, 'complete_calls': go.complete.call_count}
    assert result['settlement_calls'] == 1 and result['complete_calls'] == 0
    print(json.dumps(result, indent=2))
    (Path(__file__).parent / 'full-review-worker-result.json').write_text(json.dumps(result, indent=2))
finally:
    provider.close()
    conn.close()

# One transient 503 reaches terminal completion even though the policy permits retries.
go = MagicMock(spec=GoGenerationClient)
go.reserve.return_value = {'reservation_id': 'retry-reservation'}
provider = MagicMock(spec=ProviderClient)
provider.call_chat_completion.side_effect = ProviderResponseError(503, 'temporarily unavailable')
conn = sqlite3.connect(':memory:', check_same_thread=False)
try:
    workflow = GenerationWorkflow(go, provider, config, SqliteSaver(conn))
    state = workflow.graph.invoke({'item_id': 'retry-item', 'job_id': 'retry-job', 'fencing_token': 1, 'input_digest': 'digest', 'fixed_input': {'required_task_families': ['meaning_recognition']}, 'status': 'processing'}, {'configurable': {'thread_id': 'retry-probe'}})
    retry_result = {'provider_calls': provider.call_chat_completion.call_count, 'final_status': state['status'], 'completion_outcome': go.complete.call_args.kwargs['outcome']}
    assert retry_result == {'provider_calls': 1, 'final_status': 'failed', 'completion_outcome': 'failed'}
    print(json.dumps(retry_result, indent=2))
    (Path(__file__).parent / 'full-review-worker-retry-result.json').write_text(json.dumps(retry_result, indent=2))
finally:
    conn.close()
