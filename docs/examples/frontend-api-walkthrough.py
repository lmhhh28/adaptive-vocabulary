"""Run documented frontend requests against an isolated production avocab process.

From repository root:
  make build
  .tools/uv/bin/uv run --directory e2e --locked python ../docs/examples/frontend-api-walkthrough.py
No user data is used; no worker or paid provider calls are started.
"""
import argparse
import base64
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys
import tempfile
import uuid

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "e2e"))
from support.http_client import APIClient
from support.lifecycle import managed_server


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=ROOT / "artifacts/frontend-api")
    parser.add_argument("--browser", action="store_true", help="Also exercise the JavaScript helper in installed Chromium")
    args = parser.parse_args()
    output = args.output.resolve() / (datetime.now(timezone.utc).strftime("run_%Y%m%d_%H%M%S_") + uuid.uuid4().hex[:8])
    output.mkdir(parents=True)
    captures = []
    status, failure = "failed", None
    browser_result = None
    try:
        with tempfile.TemporaryDirectory(prefix="avocab-frontend-docs-") as tmp:
            with managed_server(ROOT / "bin/avocab", Path(tmp), seed="core", log_dir=output,
                                policy_path=ROOT / "config/policy-v2.json") as server:
                client = APIClient(server.base_url)
                try:
                    client.initialize_client_context()  # Never write the token or cookie to artifacts.

                    def call(method, path, body=None, expected=200, key=None, **kwargs):
                        if method == "GET":
                            response = client.get(path, **kwargs)
                        else:
                            response = client.post(path, json=body,
                                headers={"Idempotency-Key": key or str(uuid.uuid4())}, **kwargs)
                        data = response.json()
                        captures.append({"method": method, "path": path, "request": body,
                                         "status": response.status_code, "response": data})
                        require(response.status_code == expected, f"{method} {path}: {response.status_code} {data}")
                        return data

                    health = call("GET", "/api/v1/health")
                    require(health["mode"] == "active", "health mode")
                    call("GET", "/api/v1/maintenance")
                    require(call("GET", "/api/v1/sessions/active")["session"] is None, "fresh active slot")
                    banks = call("GET", "/api/v1/banks")["items"]
                    seed_bank = next(b for b in banks if b["id"] == "00000000-0000-4000-8000-000000000801")
                    bank = call("GET", f"/api/v1/banks/{seed_bank['id']}")["bank"]
                    call("GET", "/api/v1/sources")
                    call("GET", "/api/v1/learning/summary")
                    call("GET", f"/api/v1/learning/senses/{bank['members'][0]['sense_id']}")

                    source_id = call("POST", "/api/v1/sources", {
                        "name": "文档示例来源", "location": "local:docs", "source_version": "v1",
                        "license": "自有内容", "usage_scope": "联调"}, expected=201)["source_id"]
                    call("GET", f"/api/v1/sources/{source_id}")
                    call("POST", f"/api/v1/sources/{source_id}/imports", {
                        "source_version": "v1", "expected_revision": 0,
                        "entries": [{"external_entry_id": "docword-1", "raw_word": "docword",
                                     "raw_translation": "文档演示词", "pos": "n."}]})
                    new_bank = call("POST", "/api/v1/banks", {"name": "文档联调题库", "note": "隔离目录"}, expected=201)
                    bid = new_bank["bank_id"]
                    imported = call("POST", f"/api/v1/banks/{bid}/imports", {
                        "words": ["docword", "bank", "qwertyuiop"], "expected_revision": 0,
                        "resolution_policy_version": "v1"})
                    items = call("GET", f"/api/v1/imports/{imported['import_id']}/items?limit=50&offset=0")
                    unresolved = next(i for i in items["items"] if i["status"] == "not_found")
                    call("POST", f"/api/v1/import-items/{unresolved['id']}/resolve", {
                        "action": "exclude", "sense_refs": [], "reason": "演示排除", "expected_revision": unresolved["revision"]})
                    ambiguous = next(i for i in items["items"] if i["status"] == "ambiguous")
                    call("POST", f"/api/v1/import-items/{ambiguous['id']}/resolve", {
                        "action": "adopt", "sense_refs": [ambiguous["candidates"][0]["sense_id"]],
                        "reason": "演示字符串数组采纳", "expected_revision": ambiguous["revision"]})
                    detail = call("GET", f"/api/v1/banks/{bid}")["bank"]
                    # Multipart field names and integer-as-string revision are part of the public example.
                    call("POST", f"/api/v1/banks/{bid}/imports", files={"file": ("words.csv", b"word,sense_id\ndocword,\n", "text/csv")},
                         data={"expected_revision": str(detail["revision"]), "resolution_policy_version": "v1"})
                    detail = call("GET", f"/api/v1/banks/{bid}")["bank"]
                    changed = call("POST", f"/api/v1/banks/{bid}/members/remove", {
                        "sense_ids": [detail["members"][0]["sense_id"]], "expected_revision": detail["revision"]})
                    call("POST", f"/api/v1/banks/{bid}/archive", {"expected_revision": changed["revision"]})

                    sid = call("POST", "/api/v1/practice-sessions", {"bank_id": seed_bank["id"]}, expected=201)["session"]["id"]
                    session = call("GET", f"/api/v1/practice-sessions/{sid}")["session"]
                    active = call("GET", "/api/v1/sessions/active")["session"]
                    require(isinstance(active["created_at"], int) and "deadline_at" in active, "active session time DTO")
                    require("deadline_at" not in session, "practice session time DTO")
                    call("GET", f"/api/v1/banks/{seed_bank['id']}", expected=409)
                    question = call("POST", f"/api/v1/practice-sessions/{sid}/next", {"expected_cursor": session["cursor"]})["question"]
                    require(set(question) == {"instance_id", "ordinal", "task_family", "stem", "options"}, "public question shape")
                    key = str(uuid.uuid4())
                    body = {"instance_id": question["instance_id"], "option_id": question["options"][0]["id"], "submission_id": key}
                    feedback = call("POST", f"/api/v1/practice-sessions/{sid}/attempts", body, key=key)
                    require(call("POST", f"/api/v1/practice-sessions/{sid}/attempts", body, key=key) == feedback, "attempt replay")
                    key = str(uuid.uuid4())
                    body = {"expected_revision": feedback["session_revision"], "reason": "ended"}
                    ended = call("POST", f"/api/v1/practice-sessions/{sid}/end", body, key=key)
                    require(call("POST", f"/api/v1/practice-sessions/{sid}/end", body, key=key) == ended, "end replay")

                    exam = call("POST", "/api/v1/exams", {"bank_id": seed_bank["id"], "question_count": 3,
                                "task_quotas": {"meaning_recognition": 1, "reverse_selection": 1, "context_selection": 1},
                                "time_limit_seconds": 1800}, expected=201)
                    eid = exam["exam_id"]
                    require(isinstance(exam["deadline"], str) and exam["deadline"].endswith("Z"), "exam deadline DTO")
                    call("GET", f"/api/v1/exams/{eid}")
                    q = exam["public_questions"][0]
                    key = str(uuid.uuid4())
                    event = {"instance_id": q["instance_id"], "action": "select", "option_id": q["options"][0]["id"],
                             "client_event_id": key, "client_sequence": 1, "expected_revision": 0}
                    accepted = call("POST", f"/api/v1/exams/{eid}/response-events", event, key=key)
                    receipt = call("GET", f"/api/v1/exams/{eid}/response-events/{key}")
                    require(receipt["accepted_revision"] == accepted["current_revision"], "event receipt")
                    gap = call("POST", f"/api/v1/exams/{eid}/telemetry-gaps", {
                        "expected_revision": accepted["current_revision"], "reason": "client_gap"})
                    key = str(uuid.uuid4())
                    call("POST", f"/api/v1/exams/{eid}/submit", {"submission_id": key,
                         "expected_revision": gap["current_revision"], "last_ack_sequence": gap["last_ack_sequence"]}, key=key)
                    report = call("GET", f"/api/v1/exams/{eid}/result")
                    omitted = [q for q in report["questions"] if q["selected_option_id"] is None]
                    require(report["total"] == 3 and len(omitted) == 2 and
                            all(q["first_correct"] is None for q in omitted), "nullable result DTO")
                    require(all(q["response_history"] == "telemetry_incomplete" for q in report["questions"]), "gap result DTO")
                    call("GET", "/api/v1/quality-findings?status=open&limit=50")
                    require(call("GET", "/api/v1/sessions/active")["session"] is None, "released active slot")
                    if args.browser:
                        os.environ.setdefault("PLAYWRIGHT_BROWSERS_PATH", str(ROOT / ".tools/playwright"))
                        from playwright.sync_api import sync_playwright
                        source = base64.b64encode((Path(__file__).parent / "frontend-client.mjs").read_bytes()).decode("ascii")
                        with sync_playwright() as playwright:
                            browser = playwright.chromium.launch(headless=True)
                            try:
                                page = browser.new_page()
                                page.goto(server.base_url)
                                browser_result = page.evaluate("""async (source) => {
                                    const api = await import('data:text/javascript;base64,' + source);
                                    await api.initializeContext();
                                    const health = await api.getJSON('/api/v1/health');
                                    const body = {name: '浏览器文档演练', note: '隔离目录'};
                                    const op = api.prepareWrite('/api/v1/banks', body);
                                    body.name = '不应影响已固定的正文';
                                    const first = await api.sendPrepared(op);
                                    const second = await api.sendPrepared(op);
                                    let conflict;
                                    try {
                                        await api.sendPrepared({...op, body: JSON.stringify({name: 'changed'})});
                                    } catch (error) {
                                        conflict = {status: error.status, code: error.code};
                                    }
                                    const detail = await api.getJSON('/api/v1/banks/' + first.data.bank_id);
                                    return {health: health.data.status, created: first.status,
                                            replayed: second.replayed, sameID: first.data.bank_id === second.data.bank_id,
                                            fixedBody: detail.data.bank.name === '浏览器文档演练', conflict};
                                }""", source)
                                require(browser_result == {"health": "ok", "created": 201, "replayed": True,
                                        "sameID": True, "fixedBody": True,
                                        "conflict": {"status": 409, "code": "IDEMPOTENCY_CONFLICT"}},
                                        f"browser helper: {browser_result}")
                            finally:
                                browser.close()
                    status = "passed"
                finally:
                    client.close()
    except Exception as exc:
        failure = f"{type(exc).__name__}: {exc}"
    report = {"status": status, "requests": len(captures), "error": failure,
              "browser_helper": browser_result, "captures": captures}
    (output / "result.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"status": status, "requests": len(captures), "result": str(output / "result.json"), "error": failure}, ensure_ascii=False))
    return 0 if status == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
