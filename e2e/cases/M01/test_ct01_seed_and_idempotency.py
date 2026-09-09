"""CT-01: Seed loading and idempotency acceptance test.
Verifies initial startup with --seed core -> browser UI check -> question & bank counts ->
restart idempotency -> unknown seed rejection.
"""
from pathlib import Path
import sqlite3
import subprocess
from playwright.sync_api import sync_playwright
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    # 1. Start fresh server with --seed core
    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server1:
        base_url = server1.base_url
        artifacts.append(str(server1.log_path))

        # 2. Browser check on /banks using Chromium
        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            b_ctx = browser.new_context()
            b_ctx.tracing.start(screenshots=True, snapshots=True)

            page = b_ctx.new_page()
            page.goto(f"{base_url}/banks")

            assert "题库管理" in page.title(), f"Unexpected page title: {page.title()}"
            cards = page.locator(".bank-card")
            card_count = cards.count()
            assert card_count == 2, f"Expected 2 bank cards in UI, found {card_count}"

            # Verify bank names
            names = [page.locator(f".bank-card:nth-child({i+1}) .bank-name").text_content().strip() for i in range(card_count)]
            assert "开发样例" in names, f"Expected '开发样例' in {names}"
            assert "跨库共享样例" in names, f"Expected '跨库共享样例' in {names}"

            # Save screenshot & trace
            screenshot_path = case_dir / "banks_browser.png"
            page.screenshot(path=str(screenshot_path))
            artifacts.append(str(screenshot_path))

            trace_path = case_dir / "trace.zip"
            b_ctx.tracing.stop(path=str(trace_path))
            artifacts.append(str(trace_path))

            browser.close()

        # 3. HTTP API verification
        client = APIClient(base_url)
        resp = client.get("/api/v1/banks")
        assert resp.status_code == 200, f"Expected 200 from GET /api/v1/banks, got {resp.status_code}"
        banks_data = resp.json()["items"]
        assert len(banks_data) == 2, f"Expected 2 banks from API, got {len(banks_data)}"

        # Find Bank 1 and Bank 2
        bank1 = next(b for b in banks_data if b["name"] == "开发样例")
        bank2 = next(b for b in banks_data if b["name"] == "跨库共享样例")

        assert bank1["member_count"] == 9, f"Expected Bank 1 to have 9 members, got {bank1['member_count']}"
        assert bank2["member_count"] == 3, f"Expected Bank 2 to have 3 members, got {bank2['member_count']}"

        # Verify task counts on Bank 1
        assert bank1["task_counts"]["meaning_recognition"] > 0
        assert bank1["task_counts"]["reverse_selection"] > 0
        assert bank1["task_counts"]["context_selection"] > 0

        # Verify Sources
        resp_sources = client.get("/api/v1/sources")
        assert resp_sources.status_code == 200
        sources = resp_sources.json()["items"]
        source_ids = {s["id"] for s in sources}
        assert "project-authored-fixture-v1" in source_ids
        assert "project-authored-exam-fixture-v1" in source_ids

        client.close()

    # 4. Direct SQLite verification of counts
    db_file = data_dir / "app.db"
    conn = sqlite3.connect(str(db_file))
    cur = conn.cursor()

    cur.execute("SELECT COUNT(*) FROM lexeme")
    lexeme_count = cur.fetchone()[0]
    assert lexeme_count == 8, f"Expected 8 lexemes, got {lexeme_count}"

    cur.execute("SELECT COUNT(*) FROM sense")
    sense_count = cur.fetchone()[0]
    assert sense_count == 9, f"Expected 9 senses, got {sense_count}"

    cur.execute("SELECT COUNT(*) FROM question_version")
    question_count = cur.fetchone()[0]
    assert question_count == 14, f"Expected 14 questions, got {question_count}"

    cur.execute("SELECT COUNT(*) FROM question_form")
    form_count = cur.fetchone()[0]
    assert form_count == 14, f"Expected 14 forms, got {form_count}"

    cur.execute("SELECT COUNT(*) FROM bank")
    b_count = cur.fetchone()[0]
    assert b_count == 2, f"Expected 2 banks, got {b_count}"

    conn.close()

    # 5. Restart server on same data dir with --seed core: must be idempotent!
    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server2:
        client2 = APIClient(server2.base_url)
        resp_b = client2.get("/api/v1/banks")
        assert resp_b.status_code == 200
        assert len(resp_b.json()["items"]) == 2
        client2.close()

    # Verify counts did not grow after re-seed
    conn2 = sqlite3.connect(str(db_file))
    cur2 = conn2.cursor()
    cur2.execute("SELECT COUNT(*) FROM lexeme")
    assert cur2.fetchone()[0] == 8
    cur2.execute("SELECT COUNT(*) FROM sense")
    assert cur2.fetchone()[0] == 9
    cur2.execute("SELECT COUNT(*) FROM question_version")
    assert cur2.fetchone()[0] == 14
    cur2.execute("SELECT COUNT(*) FROM bank")
    assert cur2.fetchone()[0] == 2
    conn2.close()

    # 6. Unknown seed must fail immediately
    bad_dir = case_dir / "bad_seed_data"
    bad_dir.mkdir(parents=True, exist_ok=True)
    proc = subprocess.run(
        [str(bin_path), "serve", "--data-dir", str(bad_dir), "--listen", "127.0.0.1:0", "--seed", "invalid_unknown_seed"],
        capture_output=True,
        text=True,
    )
    assert proc.returncode != 0, f"Expected failure for unknown seed, got exit code {proc.returncode}"

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "lexemes": lexeme_count,
            "senses": sense_count,
            "questions": question_count,
            "banks": b_count,
            "idempotency": "verified",
        },
    }
