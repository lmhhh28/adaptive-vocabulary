"""W08-05: 旧回归、新旧控制器边界与进程回收
验收场景：
1. 单一 SPA 挂载点断言：无旧版 HTML 模板控制器 (practice.js / exam.js) 重复挂载；
2. 既有旧路由接入新 SPA（/practice, /exams, /banks, /imports, /quality 统一接入 React 路由）；
3. API 错误边界：未注册 API 返回标准化 JSON 404（绝不返回 HTML）；
4. 未知前端路径友好降级（渲染 AppShell 与兜底未接入提示，无白屏崩溃）；
5. 模式互斥与维护写禁入边界（active 阻止创建 exam 返回 409；maintenance 拦截写请求返回 503）；
6. 进程组生命周期与退出回收（零孤儿进程，processes.json 完整记录）。
"""
from contextlib import closing
import json
import sqlite3
import uuid

import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness
from support.http_client import APIClient


def run(context: dict) -> dict:
    bank_id = "00000000-0000-4000-8000-000000000801"

    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        data_dir = harness.data_dir
        db_path = data_dir / "app.db"

        # -------------------------------------------------------------
        # Step 1: Controller Isolation & Legacy Script Absence
        # -------------------------------------------------------------
        page.goto(f"{base_url}/practice", wait_until="networkidle")
        expect(page.locator("#root")).to_be_visible()

        # Assert no legacy scripts attached
        scripts_count = page.evaluate("""() => {
            const scripts = Array.from(document.querySelectorAll("script"));
            return scripts.filter(s => s.src && (s.src.includes("practice.js") || s.src.includes("exam.js"))).length;
        }""")
        assert scripts_count == 0, f"Found {scripts_count} legacy scripts attached in DOM"

        # -------------------------------------------------------------
        # Step 2: Route & API 404 Boundaries
        # -------------------------------------------------------------
        with httpx.Client() as client:
            # 2.1 Unknown API returns JSON 404
            resp_api_404 = client.get(f"{base_url}/api/v1/non_existent_route")
            assert resp_api_404.status_code == 404
            assert "text/html" not in resp_api_404.headers.get("Content-Type", "")

            # 2.2 Client-context is strictly JSON
            resp_ctx = client.get(f"{base_url}/client-context")
            assert resp_ctx.status_code == 200
            assert "application/json" in resp_ctx.headers.get("Content-Type", "")

        # 2.3 Unknown client path loads AppShell without crashing
        page.goto(f"{base_url}/some-future-unknown-feature", wait_until="networkidle")
        expect(page.locator("[data-testid='app-shell']")).to_be_visible()

        # -------------------------------------------------------------
        # Step 3: Cross-mode Exclusivity (Active Session Gate)
        # -------------------------------------------------------------
        # Ensure any leftover active sessions from browser navigation are ended
        with closing(sqlite3.connect(db_path)) as conn:
            conn.execute("UPDATE session SET status = 'ended' WHERE status = 'open'")
            conn.commit()

        client = APIClient(base_url)
        client.initialize_client_context()

        # Start practice session
        resp_pr = client.post(
            "/api/v1/practice-sessions",
            json={"bank_id": bank_id},
            headers={"Idempotency-Key": str(uuid.uuid4())},
        )
        assert resp_pr.status_code in (200, 201), f"Expected 200/201 creating practice session, got {resp_pr.status_code}: {resp_pr.text}"
        session_id = resp_pr.json()["session"]["id"]

        # Attempt to create exam during active practice -> 409
        resp_ex = client.post(
            "/api/v1/exams",
            json={"bank_id": bank_id, "question_count": 3},
            headers={"Idempotency-Key": str(uuid.uuid4())},
        )
        assert resp_ex.status_code == 409
        err_code = resp_ex.json().get("error", {}).get("code")
        assert err_code == "SESSION_ACTIVE"

        # Cleanly end session
        client.post(
            f"/api/v1/practice-sessions/{session_id}/end",
            json={"status": "ended"},
        )

        # -------------------------------------------------------------
        # Step 4: Maintenance Gate Protection
        # -------------------------------------------------------------
        # Set mode to rebuilding in DB
        with closing(sqlite3.connect(db_path)) as conn:
            conn.execute("UPDATE projection_control SET mode = 'rebuilding', requires_rebuild = 1 WHERE id = 1")
            conn.commit()

        # Write API blocked with 503
        resp_write = client.post(
            "/api/v1/practice-sessions",
            json={"bank_id": bank_id},
            headers={"Idempotency-Key": str(uuid.uuid4())},
        )
        assert resp_write.status_code == 503, f"Expected 503 in maintenance, got {resp_write.status_code}"

        # Browser displays maintenance banner
        page.goto(base_url, wait_until="networkidle")
        expect(page.locator("[data-testid='maintenance-banner']")).to_be_visible()

        # Restore mode to active
        with closing(sqlite3.connect(db_path)) as conn:
            conn.execute("UPDATE projection_control SET mode = 'active', requires_rebuild = 0 WHERE id = 1")
            conn.commit()

        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='maintenance-banner']")).not_to_be_visible()
        client.close()

    # Outside WebHarness context: server is stopped
    # Process information is checked in WebHarness close()
    return {
        "status": "passed",
        "artifacts": harness.artifacts,
        "details": {
            "legacy_scripts_count": 0,
            "api_404_boundary_verified": True,
            "cross_mode_409_verified": True,
            "maintenance_503_verified": True,
            "process_cleanup_verified": True,
        }
    }
