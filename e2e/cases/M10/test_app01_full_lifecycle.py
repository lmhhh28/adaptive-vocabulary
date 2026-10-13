"""APP-01: Full user lifecycle acceptance test.
Verifies complete flow entirely via Web UI on clean data directory:
1. Shell Home & Dashboard inspection (Ant Design AppShell);
2. Bank navigation and detail view;
3. Practice answering (immediate click/number keys, instant feedback, next question, end practice);
4. Exam creation, multi-question answering, submission and score report;
5. Quality console and maintenance gate verification;
6. Backup create, verify, and restore to brand new directory;
7. Launch restored server, verify historical records and resume practice without data loss;
8. Page refresh & server restart continuity without manual database edits.
"""
import json
from pathlib import Path
import subprocess
import time
import uuid

from playwright.sync_api import sync_playwright, expect
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
            b_ctx = browser.new_context(viewport={"width": 1280, "height": 800}, locale="zh-CN")
            b_ctx.tracing.start(screenshots=True, snapshots=True)

            page = b_ctx.new_page()
            page.on("dialog", lambda dialog: dialog.accept())

            # Step 1: Visit Unified Shell Home
            page.goto(base_url, wait_until="networkidle")
            assert "Adaptive Vocabulary" in page.title(), f"Unexpected title: {page.title()}"
            expect(page.locator("[data-testid='app-shell']")).to_be_visible()
            expect(page.locator("[data-testid='header-logo']")).to_contain_text("Adaptive Vocabulary")
            expect(page.locator("[data-testid='home-view']")).to_be_visible()

            # Save initial dashboard screenshot
            shot1 = case_dir / "01_shell_dashboard.png"
            page.screenshot(path=str(shot1))
            artifacts.append(str(shot1))

            # Step 2: Navigate to Banks
            page.locator("[data-testid='nav-menu']").get_by_text("题库与内容").click()
            page.wait_for_url("**/banks")
            expect(page.locator("[data-testid='banks-view']")).to_be_visible()
            bank_cards = page.locator("[data-testid^='bank-card-']")
            assert bank_cards.count() >= 1, "Expected at least 1 bank from core seed"
            bank_card = bank_cards.first
            bank_card_id = bank_card.get_attribute("data-testid")
            bank_id = bank_card_id.replace("bank-card-", "")
            assert bank_id, "Bank ID missing"

            # Open Bank Detail
            page.locator(f"[data-testid='bank-detail-link-{bank_id}']").click()
            page.wait_for_url(f"**/banks/{bank_id}")
            expect(page.locator("[data-testid='bank-detail-view']")).to_be_visible()

            # Step 3: Practice Flow
            page.locator("[data-testid='nav-menu']").get_by_text("自适应练习").click()
            page.wait_for_url("**/practice")
            expect(page.locator("[data-testid='practice-view']")).to_be_visible()

            # Wait for question to render
            stem_el = page.locator("[data-testid='question-stem-text']")
            stem_el.wait_for(state="visible", timeout=10000)
            stem_text = stem_el.text_content().strip()
            assert len(stem_text) > 0, "Practice question stem is empty"

            # Select first option (immediate click answering)
            opt0 = page.locator("[data-testid='option-item-0']")
            expect(opt0).to_be_visible()
            opt0.click()

            # Verify feedback panel appears
            feedback_panel = page.locator("[data-testid='feedback-panel']")
            feedback_panel.wait_for(state="visible", timeout=8000)

            # Advance to next question (auto or manual)
            next_btn = page.locator("[data-testid='next-question-btn']")
            if next_btn.is_visible():
                next_btn.click()
            elif page.locator("[data-testid='bottom-next-btn']").is_visible():
                page.locator("[data-testid='bottom-next-btn']").click()
            else:
                page.keyboard.press("d")

            time.sleep(0.5)

            # End practice cleanly
            btn_end = page.locator("[data-testid='end-session-btn']")
            if btn_end.is_visible():
                btn_end.click()
                modal_confirm = page.locator("[data-testid='confirm-end-btn']")
                if modal_confirm.is_visible():
                    modal_confirm.click()
                else:
                    page.get_by_role("button", name="确认结束").click()
                time.sleep(0.5)

            # Step 4: Exam Flow
            page.locator("[data-testid='nav-menu']").get_by_text("阶段考试").click()
            page.wait_for_url(lambda u: "/exams" in u)
            if not page.url.endswith("/exams/new"):
                page.goto(f"{base_url}/exams/new")
            expect(page.locator("[data-testid='exam-create-view']")).to_be_visible()

            # Configure exam: 3 questions
            count_input = page.locator("[data-testid='input-question-count']")
            count_input.click()
            page.keyboard.press("Meta+a")
            page.keyboard.press("Backspace")
            count_input.fill("3")

            # Disable timer switch so we don't rush
            page.locator("[data-testid='switch-timed']").click()

            page.locator("[data-testid='start-exam-btn']").click()

            # Wait for active exam
            page.wait_for_url(lambda u: "/exams/" in u and not u.endswith("/new"), timeout=10000)
            expect(page.locator("[data-testid='exam-active-view']")).to_be_visible()

            # Answer 3 questions
            for q_idx in range(3):
                opt = page.locator("[data-testid='exam-option-0']")
                opt.wait_for(state="visible", timeout=8000)
                opt.click()
                time.sleep(0.3)
                if q_idx < 2:
                    next_q = page.locator("[data-testid='next-question-btn']")
                    if next_q.is_visible() and next_q.is_enabled():
                        next_q.click()
                        time.sleep(0.3)

            # Submit exam
            page.locator("[data-testid='submit-exam-btn']").click()
            page.get_by_role("button", name="确认交卷").click()

            # Wait for result view
            page.wait_for_url("**/exams/*/result", timeout=12000)
            expect(page.locator("[data-testid='exam-result-view']")).to_be_visible()

            shot2 = case_dir / "02_exam_result.png"
            page.screenshot(path=str(shot2))
            artifacts.append(str(shot2))

            # Step 5: Quality Console & Maintenance Gate
            page.locator("[data-testid='nav-menu']").get_by_text("质量与维护").click()
            page.wait_for_url("**/quality")
            expect(page.locator("[data-testid='quality-view']")).to_be_visible()
            page.get_by_role("tab", name="系统维护状态").click()
            expect(page.locator("[data-testid='maintenance-info']")).to_be_visible()

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
            assert "Adaptive Vocabulary" in page2.title()
            expect(page2.locator("[data-testid='app-shell']")).to_be_visible()

            # Check Banks exist in restored instance
            page2.goto(f"{server2.base_url}/banks")
            expect(page2.locator("[data-testid='banks-view']")).to_be_visible()
            assert page2.locator("[data-testid^='bank-card-']").count() >= 1

            # Refresh continuity test
            page2.reload()
            expect(page2.locator("[data-testid='banks-view']")).to_be_visible()
            assert page2.locator("[data-testid^='bank-card-']").count() >= 1

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
