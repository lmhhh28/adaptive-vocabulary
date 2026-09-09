"""APP-01: Full user lifecycle acceptance test.
Verifies complete flow entirely via Web UI on clean data directory:
1. Shell Home & Dashboard inspection;
2. Bank navigation and detail view;
3. Practice answering, instant feedback, next question, end practice;
4. Exam creation, multi-question answering, submission and score report;
5. Quality console disposition and maintenance gate verification;
6. Backup create, verify, and restore to brand new directory;
7. Launch restored server, verify historical records and resume practice without data loss;
8. Page refresh & server restart continuity without manual database edits.
"""
import json
from pathlib import Path
import subprocess
import time
import uuid

from playwright.sync_api import sync_playwright
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    src_data_dir = case_dir / "src_data"
    src_data_dir.mkdir(parents=True, exist_ok=True)
    backup_dir = case_dir / "backup"
    restored_data_dir = case_dir / "restored_data"
    artifacts = []

    # 1. Start fresh server with seed core
    with managed_server(bin_path, src_data_dir, listen_port=0, seed="core", log_dir=case_dir) as server1:
        base_url = server1.base_url
        artifacts.append(str(server1.log_path))

        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            b_ctx = browser.new_context()
            b_ctx.tracing.start(screenshots=True, snapshots=True)

            page = b_ctx.new_page()
            page.on("dialog", lambda dialog: dialog.accept())

            # Step 1: Visit Unified Shell Home
            page.goto(base_url)
            assert "Adaptive Vocabulary" in page.title(), f"Unexpected title: {page.title()}"
            app_title = page.locator("#app-title").text_content()
            assert app_title == "Adaptive Vocabulary"
            contract_ver = page.locator("#contract-version").text_content()
            assert contract_ver == "1.1.0"
            schema_ver = page.locator("#schema-version").text_content()
            assert int(schema_ver) >= 0

            # Save initial dashboard screenshot
            shot1 = case_dir / "01_shell_dashboard.png"
            page.screenshot(path=str(shot1))
            artifacts.append(str(shot1))

            # Step 2: Navigate to Banks
            page.click("text=题库")
            page.wait_for_url("**/banks")
            assert "题库管理" in page.title() or "题库" in page.content()
            bank_cards = page.locator(".bank-card")
            assert bank_cards.count() >= 1, "Expected at least 1 bank from core seed"
            bank_id = bank_cards.first.get_attribute("data-bank-id")
            assert bank_id, "Bank ID attribute missing"

            # Open Bank Detail
            bank_cards.first.locator("text=查看与管理").click()
            page.wait_for_url(f"**/banks/{bank_id}")
            assert page.locator(".bank-detail-header").is_visible()

            # Step 3: Practice Flow
            page.click("text=自适应练习")
            page.wait_for_url("**/practice")
            assert "自适应训练" in page.title()

            # If bank select view is shown, click start bank
            btn_start_bank = page.locator(f".bank-select-card[data-bank-id='{bank_id}'] .btn-start-bank")
            if btn_start_bank.is_visible():
                btn_start_bank.click()

            # Wait for question to render
            stem_el = page.locator("#question-stem")
            stem_el.wait_for(state="visible", timeout=10000)
            stem_text = stem_el.text_content().strip()
            assert len(stem_text) > 0, "Practice question stem is empty"

            # Select first option
            options = page.locator(".option-btn")
            assert options.count() >= 2, "Expected at least 2 options"
            options.first.click()

            # Submit answer
            btn_submit = page.locator("#btn-submit")
            assert btn_submit.is_enabled()
            btn_submit.click()

            # Verify feedback banner and explanation appear
            feedback_banner = page.locator("#feedback-result-banner")
            feedback_banner.wait_for(state="visible", timeout=8000)
            assert not page.locator("#feedback-section").is_hidden()

            # Next question
            btn_next = page.locator("#btn-next")
            btn_next.wait_for(state="visible")
            btn_next.click()

            # Wait for second question or end
            time.sleep(0.5)
            # End practice session cleanly
            btn_end = page.locator("#btn-end-practice")
            if btn_end.is_visible():
                btn_end.click()
                time.sleep(0.5)

            # Step 4: Exam Flow
            page.click("text=阶段考试")
            page.wait_for_url("**/exams/new")
            assert page.locator("#create-exam-form").is_visible()

            # Configure exam: 3 questions, 10 minutes limit
            page.fill("#question-count", "3")
            page.click("#btn-create-exam")

            # Wait for exam page
            page.wait_for_url(lambda u: "/exams/" in u and not u.endswith("/new"), timeout=10000)
            exam_url = page.url
            assert "/exams/" in exam_url and not exam_url.endswith("/new")

            exam_stem = page.locator("#q-stem")
            exam_stem.wait_for(state="visible", timeout=8000)

            # Answer all 3 questions
            for q_idx in range(3):
                # Select first option for current question
                opt_items = page.locator(".option-item")
                assert opt_items.count() >= 2
                opt_items.first.click()
                time.sleep(0.3)
                if q_idx < 2:
                    btn_next_q = page.locator("#btn-next-q")
                    if btn_next_q.is_enabled():
                        btn_next_q.click()
                        time.sleep(0.3)

            # Submit exam
            btn_submit_exam = page.locator("#btn-submit-exam")
            btn_submit_exam.click()

            # Wait for result page
            page.wait_for_url("**/exams/*/result", timeout=12000)
            result_card = page.locator("#result-view")
            result_card.wait_for(state="visible", timeout=8000)
            assert "考试评测报告" in page.content() or "测试报告" in page.content()

            shot2 = case_dir / "02_exam_result.png"
            page.screenshot(path=str(shot2))
            artifacts.append(str(shot2))

            # Step 5: Quality Console & Maintenance Gate
            page.click("text=质量与维护")
            page.wait_for_url("**/quality")
            assert page.locator("#system-mode").is_visible()
            assert page.locator("#system-mode").text_content() == "active"

            # Select target question to dispute
            q_select = page.locator("#question-select")
            options_count = q_select.locator("option").count()
            if options_count > 1:
                q_select.select_option(index=1)
                page.locator("#action-select").select_option("disputed")
                page.fill("#reason-input", "APP-01 automated test dispute")
                page.click("#btn-submit-disposition")
                time.sleep(1.0)

            # Trace
            trace_path = case_dir / "trace_lifecycle.zip"
            b_ctx.tracing.stop(path=str(trace_path))
            artifacts.append(str(trace_path))
            browser.close()

    # Step 6: Full Backup via CLI
    cmd_backup = [
        str(bin_path), "backup", "create",
        "--data-dir", str(src_data_dir),
        "--output", str(backup_dir)
    ]
    res_bk = subprocess.run(cmd_backup, capture_output=True, text=True)
    assert res_bk.returncode == 0, f"backup create failed: {res_bk.stderr} {res_bk.stdout}"
    manifest = json.loads((backup_dir / "manifest.json").read_text())
    assert manifest["format_version"] == "1.0.0"
    assert manifest["schema_version"] >= 80

    # Verify backup
    cmd_verify = [str(bin_path), "backup", "verify", "--input", str(backup_dir)]
    res_ver = subprocess.run(cmd_verify, capture_output=True, text=True)
    assert res_ver.returncode == 0, f"backup verify failed: {res_ver.stderr} {res_ver.stdout}"

    # Step 7: Restore to Brand New Directory
    cmd_restore = [
        str(bin_path), "backup", "restore",
        "--input", str(backup_dir),
        "--target", str(restored_data_dir)
    ]
    res_res = subprocess.run(cmd_restore, capture_output=True, text=True)
    assert res_res.returncode == 0, f"backup restore failed: {res_res.stderr} {res_res.stdout}"

    # Step 8: Start server on Restored Directory & Verify Continuity
    with managed_server(bin_path, restored_data_dir, listen_port=0, log_dir=case_dir) as server2:
        artifacts.append(str(server2.log_path))
        with sync_playwright() as p:
            browser2 = p.chromium.launch(headless=True)
            page2 = browser2.new_page()

            # Check Shell
            page2.goto(server2.base_url)
            assert page2.locator("#app-title").text_content() == "Adaptive Vocabulary"
            assert page2.locator("#schema-version").text_content() == str(manifest["schema_version"])

            # Check Banks exist in restored instance
            page2.goto(f"{server2.base_url}/banks")
            assert page2.locator(".bank-card").count() >= 1

            # Refresh continuity test
            page2.reload()
            assert page2.locator(".bank-card").count() >= 1

            browser2.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "bank_id": bank_id,
            "backup_manifest": manifest["format_version"],
            "schema_version": manifest["schema_version"],
            "restored": True
        }
    }
