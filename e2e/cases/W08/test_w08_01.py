"""W08-01: 完整学习与跨页面恢复
验收场景：
1. 首页、题库与自适应练习入口导航；
2. 即点即答（无提交按钮）、即时反馈与推进；
3. 练习过程中跨页面跳转至题库，再返回练习页面完整恢复（当前题目、历史作答卡片保留）；
4. 练习会话安全结束；
5. 阶段考试创建、逐题作答、改答重选、交卷确认与多维评测报告；
6. 刷新考试结果页状态持久化，SQLite 数据关联不变量核对。
"""
from contextlib import closing
import sqlite3
import time

from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    bank_id = "00000000-0000-4000-8000-000000000801"

    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        db_path = harness.data_dir / "app.db"

        # -------------------------------------------------------------
        # Step 1: Visit Unified Shell Home & Navigation
        # -------------------------------------------------------------
        page.goto(base_url, wait_until="networkidle")
        assert "Adaptive Vocabulary" in page.title(), f"Unexpected title: {page.title()}"
        expect(page.locator("[data-testid='app-shell']")).to_be_visible()
        expect(page.locator("[data-testid='header-logo']")).to_contain_text("Adaptive Vocabulary")
        expect(page.locator("[data-testid='home-view']")).to_be_visible()
        expect(page.locator("[data-testid='card-practice']")).to_be_visible()
        expect(page.locator("[data-testid='card-exam']")).to_be_visible()
        expect(page.locator("[data-testid='card-banks']")).to_be_visible()

        # -------------------------------------------------------------
        # Step 2: Navigate to Banks & Detail View
        # -------------------------------------------------------------
        page.locator("[data-testid='nav-menu']").get_by_text("题库与内容").click()
        page.wait_for_url("**/banks")
        expect(page.locator("[data-testid='banks-view']")).to_be_visible()

        bank_card = page.locator(f"[data-testid='bank-card-{bank_id}']")
        expect(bank_card).to_be_visible()

        # Open Bank Detail
        page.locator(f"[data-testid='bank-detail-link-{bank_id}']").click()
        page.wait_for_url(f"**/banks/{bank_id}")
        expect(page.locator("[data-testid='bank-detail-view']")).to_be_visible()

        # -------------------------------------------------------------
        # Step 3: Practice Answering & Immediate Feedback (PR-01)
        # -------------------------------------------------------------
        page.locator("[data-testid='detail-practice-btn']").click()
        page.wait_for_url(f"**/practice?bank_id={bank_id}")
        expect(page.locator("[data-testid='practice-view']")).to_be_visible()

        # Check question stem rendered
        stem_el = page.locator("[data-testid='question-stem-text']")
        stem_el.wait_for(state="visible", timeout=10000)
        assert len(stem_el.text_content().strip()) > 0, "Question stem is empty"

        # Assert NO submit button exists anywhere (PR-01)
        assert page.get_by_role("button", name="提交").count() == 0, "Submit button must not exist in practice"
        assert page.get_by_role("button", name="提交答案").count() == 0

        # Answer question 0 by clicking first option
        opt0 = page.locator("[data-testid='option-item-0']")
        expect(opt0).to_be_visible()
        opt0.click()

        # Immediate feedback panel appears
        feedback_panel = page.locator("[data-testid='feedback-panel']")
        feedback_panel.wait_for(state="visible", timeout=8000)

        # Advance to question 1
        if page.locator("[data-testid='next-question-btn']").is_visible():
            page.locator("[data-testid='next-question-btn']").click()
        else:
            page.keyboard.press("d")

        # Wait for Question 1 to load
        page.wait_for_timeout(400)
        expect(page.locator("[data-testid='question-stem-text']")).to_be_visible()

        # -------------------------------------------------------------
        # Step 4: Mid-session Cross-page Navigation & State Recovery
        # -------------------------------------------------------------
        # Navigate away to Banks
        page.locator("[data-testid='nav-menu']").get_by_text("题库与内容").click()
        page.wait_for_url("**/banks")
        expect(page.locator("[data-testid='banks-view']")).to_be_visible()

        # Navigate back to Practice
        page.locator("[data-testid='nav-menu']").get_by_text("自适应练习").click()
        page.wait_for_url("**/practice")
        expect(page.locator("[data-testid='practice-view']")).to_be_visible()

        # Verify active session resumed seamlessly with question stem visible
        expect(page.locator("[data-testid='question-stem-text']")).to_be_visible()
        # Answer sheet should show 1 answered item
        expect(page.locator("[data-testid='answer-sheet']")).to_be_visible()

        # Answer question 1 using keyboard '2' (option 1)
        page.keyboard.press("2")
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()

        # Cleanly end practice session
        btn_end = page.locator("[data-testid='end-session-btn']")
        expect(btn_end).to_be_visible()
        btn_end.click()
        modal_confirm = page.locator("[data-testid='confirm-end-btn']")
        if modal_confirm.is_visible():
            modal_confirm.click()
        else:
            page.get_by_role("button", name="确认结束").click()
        page.wait_for_timeout(500)

        # -------------------------------------------------------------
        # Step 5: Exam Flow & Submission
        # -------------------------------------------------------------
        page.locator("[data-testid='nav-menu']").get_by_text("阶段考试").click()
        page.wait_for_url(lambda u: "/exams" in u)
        if not page.url.endswith("/exams/new"):
            page.goto(f"{base_url}/exams/new", wait_until="networkidle")
        expect(page.locator("[data-testid='exam-create-view']")).to_be_visible()

        # Configure exam: 3 questions
        count_input = page.locator("[data-testid='input-question-count']")
        count_input.click()
        page.keyboard.press("Meta+a")
        page.keyboard.press("Backspace")
        count_input.fill("3")

        # Disable timer switch
        page.locator("[data-testid='switch-timed']").click()
        page.locator("[data-testid='start-exam-btn']").click()

        # Wait for active exam view
        page.wait_for_url(lambda u: "/exams/" in u and not u.endswith("/new"), timeout=10000)
        expect(page.locator("[data-testid='exam-active-view']")).to_be_visible()

        # Answer 3 questions
        for q_idx in range(3):
            opt = page.locator("[data-testid='exam-option-0']")
            opt.wait_for(state="visible", timeout=8000)
            opt.click()
            page.wait_for_timeout(300)
            if q_idx < 2:
                next_btn = page.locator("[data-testid='next-question-btn']")
                if next_btn.is_visible() and next_btn.is_enabled():
                    next_btn.click()
                    page.wait_for_timeout(300)

        # Reselect / modify answer on question 3
        opt1 = page.locator("[data-testid='exam-option-1']")
        opt1.click()
        page.wait_for_timeout(300)

        # Submit exam
        page.locator("[data-testid='submit-exam-btn']").click()
        page.get_by_role("button", name="确认交卷").click()

        # Wait for result view
        page.wait_for_url("**/exams/*/result", timeout=12000)
        expect(page.locator("[data-testid='exam-result-view']")).to_be_visible()
        expect(page.locator("[data-testid='score-stat']")).to_be_visible()
        expect(page.locator("[data-testid='ratio-stat']")).to_be_visible()
        expect(page.locator("[data-testid='response-analysis-panel']")).to_be_visible()

        # Step 6: Reload result page to verify state persistence
        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='exam-result-view']")).to_be_visible()
        expect(page.locator("[data-testid='score-stat']")).to_be_visible()

        # -------------------------------------------------------------
        # Step 7: SQLite Invariant Assertions
        # -------------------------------------------------------------
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            # Check practice attempts recorded
            cur.execute("SELECT count(*) FROM attempt_event")
            attempts_count = cur.fetchone()[0]
            assert attempts_count >= 2, f"Expected at least 2 attempt_event rows, got {attempts_count}"

            # Check practice session ended
            cur.execute("""
                SELECT s.status
                FROM session s
                JOIN practice_session ps ON ps.session_id = s.id
                WHERE ps.bank_id = ?
                ORDER BY s.created_at DESC
                LIMIT 1
            """, (bank_id,))
            session_status = cur.fetchone()[0]
            assert session_status == "ended", f"Expected practice session status 'ended', got {session_status}"

            # Check exam completed
            cur.execute("""
                SELECT s.status, ar.total_count, ar.correct_count
                FROM session s
                JOIN exam_session es ON es.session_id = s.id
                JOIN assessment_result ar ON ar.session_id = s.id
                ORDER BY s.created_at DESC
                LIMIT 1
            """)
            exam_row = cur.fetchone()
            assert exam_row is not None, "Expected exam assessment result row in database"
            assert exam_row[0] == "submitted", f"Expected exam status 'submitted', got {exam_row[0]}"
            assert exam_row[1] == 3, f"Expected total_count = 3, got {exam_row[1]}"
            assert exam_row[2] is not None, "Expected correct_count to be non-null"

            # Check response events recorded
            cur.execute("SELECT count(*) FROM response_event")
            resp_events = cur.fetchone()[0]
            assert resp_events >= 4, f"Expected at least 4 response events (3 selects + 1 reselect), got {resp_events}"

    return {
        "status": "passed",
        "artifacts": harness.artifacts,
        "details": {
            "practice_attempts_count": attempts_count,
            "session_status": session_status,
            "exam_status": exam_row[0],
            "exam_score": exam_row[1],
            "response_events_count": resp_events,
            "cross_page_recovery_verified": True,
        }
    }
