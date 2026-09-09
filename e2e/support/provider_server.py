"""Determinstic HTTP mock provider server for testing generation and evaluation workflows.
Runs on loopback 127.0.0.1:0 with dynamic port allocation.
Implements POST /v1/chat/completions adhering to the OpenAI JSON completion specification.
"""
from contextlib import contextmanager
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import threading
import time
import uuid

FIXTURES_DIR = Path(__file__).resolve().parents[1] / "fixtures" / "provider"


def load_fixture(name: str) -> dict:
    path = FIXTURES_DIR / name
    if not path.is_file():
        raise FileNotFoundError(f"Fixture {name} not found in {FIXTURES_DIR}")
    return json.loads(path.read_text(encoding="utf-8"))


class ProviderHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        # Silence default stderr logging
        pass

    def do_POST(self):
        server: "ProviderServer" = getattr(self.server, "provider_server", None)  # type: ignore
        if server is None:
            self.send_response(500)
            self.end_headers()
            return

        if self.path != "/v1/chat/completions" and self.path != "/chat/completions":
            self.send_response(404)
            self.end_headers()
            return

        auth = self.headers.get("Authorization", "")
        if server.expected_api_key and auth != f"Bearer {server.expected_api_key}":
            self.send_response(401)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error": "unauthorized"}')
            return

        content_len = int(self.headers.get("Content-Length", 0))
        body_raw = self.rfile.read(content_len)
        try:
            req_data = json.loads(body_raw.decode("utf-8"))
        except Exception:
            req_data = {}

        with server.lock:
            server.request_count += 1
            call_idx = server.request_count
            server.requests_log.append({
                "index": call_idx,
                "path": self.path,
                "headers": dict(self.headers),
                "body": req_data
            })

            # Check if fault injection is active
            if server.fault_mode == "network_error":
                self.send_response(500)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(b'{"error": "internal provider error"}')
                return

            if server.fault_mode == "timeout":
                time.sleep(server.timeout_seconds)
                # Client may have timed out already
                self.send_response(200)
                self.end_headers()
                return

            # Check queued responses
            if server.response_queue:
                resp_item = server.response_queue.pop(0)
                status_code = resp_item.get("status_code", 200)
                payload = resp_item.get("payload", {})
                usage = resp_item.get("usage", {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150})
            else:
                # Automatic scenario based on prompt / scenario_mode
                status_code = 200
                scenario = server.scenario_mode
                is_evaluator = any("evaluat" in str(msg.get("content", "")).lower() for msg in req_data.get("messages", []))

                if scenario == "revision":
                    # First generation is flawed, first eval is reject, second gen revised, second eval pass
                    if not is_evaluator:
                        if server.generator_call_count == 0:
                            payload = load_fixture("candidate_flawed.json")
                        else:
                            payload = load_fixture("candidate_revised.json")
                        server.generator_call_count += 1
                    else:
                        if server.evaluator_call_count < 2:
                            payload = load_fixture("eval_reject.json")
                        else:
                            payload = load_fixture("eval_pass.json")
                        server.evaluator_call_count += 1
                elif scenario == "uncertain":
                    if not is_evaluator:
                        payload = load_fixture("candidate_pass.json")
                        server.generator_call_count += 1
                    else:
                        payload = load_fixture("eval_uncertain.json")
                        server.evaluator_call_count += 1
                elif scenario == "conflict":
                    if not is_evaluator:
                        payload = load_fixture("candidate_pass.json")
                        server.generator_call_count += 1
                    else:
                        if server.evaluator_call_count % 2 == 0:
                            payload = load_fixture("eval_pass.json")
                        else:
                            payload = load_fixture("eval_reject.json")
                        server.evaluator_call_count += 1
                elif scenario == "always_reject":
                    if not is_evaluator:
                        payload = load_fixture("candidate_flawed.json")
                        server.generator_call_count += 1
                    else:
                        payload = load_fixture("eval_reject.json")
                        server.evaluator_call_count += 1
                else:
                    # Default: normal_pass
                    if not is_evaluator:
                        payload = load_fixture("candidate_pass.json")
                        server.generator_call_count += 1
                    else:
                        payload = load_fixture("eval_pass.json")
                        server.evaluator_call_count += 1

                if server.omit_usage:
                    usage = None
                else:
                    usage = {"prompt_tokens": 120, "completion_tokens": 80, "total_tokens": 200}

        content_str = json.dumps(payload, ensure_ascii=False)
        response_dict = {
            "id": f"chatcmpl-{uuid.uuid4().hex[:12]}",
            "object": "chat.completion",
            "created": int(time.time()),
            "model": req_data.get("model", "test-model"),
            "choices": [
                {
                    "index": 0,
                    "message": {
                        "role": "assistant",
                        "content": content_str
                    },
                    "finish_reason": "stop"
                }
            ]
        }
        if usage is not None:
            response_dict["usage"] = usage

        resp_bytes = json.dumps(response_dict, ensure_ascii=False).encode("utf-8")
        self.send_response(status_code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(resp_bytes)))
        self.end_headers()
        self.wfile.write(resp_bytes)


class ProviderServer:
    def __init__(
        self,
        expected_api_key: str = "test-api-key",
        scenario_mode: str = "normal_pass"
    ):
        self.expected_api_key = expected_api_key
        self.scenario_mode = scenario_mode
        self.fault_mode: str | None = None
        self.timeout_seconds: float = 5.0
        self.omit_usage: bool = False
        self.response_queue: list[dict] = []
        self.requests_log: list[dict] = []
        self.request_count: int = 0
        self.generator_call_count: int = 0
        self.evaluator_call_count: int = 0
        self.lock = threading.Lock()

        self._httpd = ThreadingHTTPServer(("127.0.0.1", 0), ProviderHandler)
        self._httpd.provider_server = self
        self.port = self._httpd.server_address[1]
        self.base_url = f"http://127.0.0.1:{self.port}"
        self._thread = threading.Thread(target=self._httpd.serve_forever, daemon=True)

    def start(self):
        self._thread.start()
        return self

    def stop(self):
        self._httpd.shutdown()
        self._httpd.server_close()
        self._thread.join(timeout=3.0)

    def queue_response(self, payload: dict, status_code: int = 200, usage: dict | None = None):
        with self.lock:
            self.response_queue.append({
                "payload": payload,
                "status_code": status_code,
                "usage": usage or {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150}
            })

    def reset_counts(self):
        with self.lock:
            self.request_count = 0
            self.generator_call_count = 0
            self.evaluator_call_count = 0
            self.requests_log.clear()
            self.response_queue.clear()


@contextmanager
def managed_provider(expected_api_key: str = "test-api-key", scenario_mode: str = "normal_pass"):
    server = ProviderServer(expected_api_key=expected_api_key, scenario_mode=scenario_mode)
    server.start()
    try:
        yield server
    finally:
        server.stop()
