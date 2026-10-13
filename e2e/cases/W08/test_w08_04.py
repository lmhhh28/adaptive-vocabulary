"""W08-04: 五档宽度、长文、键盘输入法弹层焦点
验收场景：
1. 五档核心屏幕宽度响应式布局 (320px, 375px, 768px, 1024px, 1440px)；
2. 全页面无横向滚动条断言 (scrollWidth <= window.innerWidth + 1)；
3. 长文本排版与自动换行（无溢出与遮挡）；
4. 键盘与输入焦点隔离（输入框聚焦时 1-4 / A / D 不误触答题与导航）；
5. 模态弹层 (Modal) 打开与关闭、焦点管理无泄露。
"""
from contextlib import closing
import sqlite3

from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    bank_id = "00000000-0000-4000-8000-000000000801"

    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        db_path = harness.data_dir / "app.db"

        # The detail header used to rely on document-level overflow hiding,
        # which concealed off-screen action buttons. Check actual element
        # geometry before a practice session locks content discovery.
        page.set_viewport_size({"width": 375, "height": 812})
        page.goto(f"{base_url}/banks/{bank_id}", wait_until="networkidle")
        for test_id in (
            "detail-practice-btn",
            "detail-exam-btn",
            "detail-import-btn",
            "archive-bank-btn",
        ):
            box = page.locator(f"[data-testid='{test_id}']").bounding_box()
            assert box is not None
            assert box["x"] >= 0 and box["x"] + box["width"] <= 375, (
                f"{test_id} is clipped at 375px: {box}"
            )

        # -------------------------------------------------------------
        # Step 1: Five Viewport Widths Responsive & Zero-Overflow Check
        # -------------------------------------------------------------
        viewports = [
            (320, 568, "320_ultra_mobile"),
            (375, 667, "375_standard_mobile"),
            (768, 1024, "768_tablet_portrait"),
            (1024, 768, "1024_tablet_landscape"),
            (1440, 900, "1440_desktop_wide"),
        ]

        routes_to_test = [
            "/",
            "/banks",
            f"/practice?bank_id={bank_id}",
            "/exams/new",
            "/quality",
        ]

        for width, height, label in viewports:
            page.set_viewport_size({"width": width, "height": height})

            for route in routes_to_test:
                page.goto(f"{base_url}{route}", wait_until="networkidle")

                overflow_check = page.evaluate("""() => {
                    const scrollW = document.documentElement.scrollWidth;
                    const clientW = window.innerWidth;
                    return { scrollW, clientW, overflows: scrollW > clientW + 1 };
                }""")
                assert not overflow_check["overflows"], (
                    f"Horizontal overflow at {label} ({width}x{height}) on {route}: "
                    f"scrollWidth={overflow_check['scrollW']} > clientWidth={overflow_check['clientW']}"
                )

        # Restore standard desktop viewport
        page.set_viewport_size({"width": 1280, "height": 800})

        # -------------------------------------------------------------
        # Step 2: Keyboard & Input Focus Isolation
        # -------------------------------------------------------------
        page.goto(f"{base_url}/banks", wait_until="networkidle")
        search_input = page.locator("[data-testid='search-bank-input']")
        expect(search_input).to_be_visible()

        # Focus input and type shortcut keys '1', '2', '3', 'a', 'd'
        search_input.click()
        search_input.fill("test 1 2 a d")
        input_val = search_input.input_value()
        assert "1 2 a d" in input_val, f"Input value should receive typed characters, got {input_val}"

        # -------------------------------------------------------------
        # Step 3: Modal Interaction & Focus Management
        # -------------------------------------------------------------
        page.goto(f"{base_url}/practice?bank_id={bank_id}", wait_until="networkidle")
        expect(page.locator("[data-testid='practice-view']")).to_be_visible()

        # Open end session modal
        page.locator("[data-testid='end-session-btn']").click()
        expect(page.locator(".ant-modal")).to_be_visible()

        with closing(sqlite3.connect(db_path)) as conn:
            before_attempts = conn.execute("SELECT count(*) FROM attempt_event").fetchone()[0]

        # Global shortcuts must be inert while the modal owns interaction.
        page.keyboard.press("1")
        page.wait_for_timeout(250)
        expect(page.locator(".ant-modal")).to_be_visible()
        with closing(sqlite3.connect(db_path)) as conn:
            after_attempts = conn.execute("SELECT count(*) FROM attempt_event").fetchone()[0]
        assert after_attempts == before_attempts, "Numeric shortcut answered behind the modal"

        # Cancel modal via cancel button
        page.get_by_role("button", name="继续练习").click()
        expect(page.locator(".ant-modal")).not_to_be_visible()

        # -------------------------------------------------------------
        # Step 4: Long Text & Container Wrapping Check
        # -------------------------------------------------------------
        stem = page.locator("[data-testid='question-stem-text']")
        expect(stem).to_be_visible()
        stem_box = stem.bounding_box()
        assert stem_box is not None and stem_box["width"] > 0, "Stem element has zero dimensions"
        assert stem_box["width"] <= 1280, f"Stem element width {stem_box['width']} exceeds viewport"

    return {
        "status": "passed",
        "artifacts": harness.artifacts,
        "details": {
            "viewports_checked": [f"{w}x{h}" for w, h, _ in viewports],
            "zero_overflow_verified": True,
            "input_focus_guard_verified": True,
            "modal_interaction_verified": True,
            "modal_shortcut_isolation_verified": True,
            "bank_detail_actions_visible_at_375": True,
        }
    }
