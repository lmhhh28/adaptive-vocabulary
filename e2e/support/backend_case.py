"""Production-binary test classes: isolated processes, HTTP evidence and DB invariants."""
from contextlib import closing
import json
from pathlib import Path
import sqlite3
import unittest
import uuid

from support.http_client import APIClient
from support.lifecycle import start_server

BANK_ID = "00000000-0000-4000-8000-000000000801"


class BackendCase(unittest.TestCase):
    context: dict

    def setUp(self):
        self.case_dir = self.context["case_dir"] / self._testMethodName
        self.data_dir = self.case_dir / "data"
        self.case_dir.mkdir(parents=True, exist_ok=True)
        self.http_events = []
        self.addCleanup(self.save_evidence)
        # These regressions must also hold in the shipped binary, without e2e hooks.
        self.bin_path = self.context["root_dir"] / "bin" / "avocab"
        self.restart(seed="core")

    def save_evidence(self):
        (self.case_dir / "http.json").write_text(
            json.dumps(self.http_events, ensure_ascii=False, indent=2) + "\n")

    def restart(self, seed=None, crash=False):
        if hasattr(self, "client"):
            self.client.close()
            if crash:
                self.server.proc.kill()
                self.server.proc.wait(timeout=5)
            else:
                self.server.stop()
        number = len(list(self.case_dir.glob("process-*"))) + 1
        self.server = start_server(self.bin_path, self.data_dir, seed=seed,
                                   log_dir=self.case_dir / f"process-{number}",
                                   policy_path=self.context["policy_path"])
        self.addCleanup(self.server.stop)
        self.client = APIClient(self.server.base_url)
        self.addCleanup(self.client.close)
        self.client.initialize_client_context()

    def request(self, method, path, body=None, key=None):
        if method == "GET":
            response = self.client.get(path)
        else:
            response = self.client.post(path, json=body,
                                        headers={"Idempotency-Key": key or str(uuid.uuid4())})
        self.http_events.append({"method": method, "path": path, "request": body,
                                 "status": response.status_code, "response": response.text,
                                 "replayed": response.headers.get("Idempotent-Replayed")})
        return response

    def post(self, path, body, status=200, key=None):
        response = self.request("POST", path, body, key)
        self.assertEqual(response.status_code, status, response.text)
        return response.json()

    def get(self, path, status=200):
        response = self.request("GET", path)
        self.assertEqual(response.status_code, status, response.text)
        return response.json()

    def error(self, response, code, status=409):
        self.assertEqual(response.status_code, status, response.text)
        self.assertEqual(response.json()["error"]["code"], code, response.text)

    def rows(self, sql, args=()):
        with closing(sqlite3.connect(f"file:{self.data_dir / 'app.db'}?mode=ro", uri=True)) as db:
            return db.execute(sql, args).fetchall()

    def fixture(self, sql, args=()):
        """Explicit fault/clock setup only; business transitions always go through HTTP."""
        with closing(sqlite3.connect(self.data_dir / "app.db")) as db, db:
            db.execute(sql, args)

    def create_exam(self, timed=False):
        payload = {"bank_id": BANK_ID, "question_count": 3}
        if timed:
            payload["time_limit_seconds"] = 60
        return self.post("/api/v1/exams", payload, 201)

    def create_practice(self):
        return self.post("/api/v1/practice-sessions", {"bank_id": BANK_ID}, 201)["session"]["id"]

    def expire_exam(self, exam_id):
        self.fixture("UPDATE session SET deadline_at = 1 WHERE id = ?", (exam_id,))
        self.fixture("UPDATE exam_session SET deadline_at = 1 WHERE session_id = ?", (exam_id,))


def run_class(case_type, context):
    case_type.context = context
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(case_type)
    log_path = context["case_dir"] / "scenarios.log"
    with log_path.open("w") as stream:
        result = unittest.TextTestRunner(stream=stream, verbosity=2).run(suite)
    failures = [{"scenario": test.id(), "traceback": trace}
                for test, trace in result.failures + result.errors]
    return {"status": "passed" if result.wasSuccessful() and not result.skipped else "failed",
            "artifacts": [str(p) for p in sorted(context["case_dir"].rglob("*.log"))] +
                         [str(p) for p in sorted(context["case_dir"].rglob("http.json"))],
            "details": {"binary": "bin/avocab (production)", "scenarios": result.testsRun,
                        "failures": failures, "skipped": len(result.skipped)}}
