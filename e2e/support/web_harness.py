"""Web E2E test harness for Adaptive Vocabulary.
Provides isolated Go server lifecycle, Playwright Chromium session with tracing,
SQLite state verification, and standardized evidence capture (processes.json, state-summary.json, trace.zip, screenshots).
"""
from contextlib import closing
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import sys
import time
from typing import Optional, Dict, Any, List

from playwright.sync_api import sync_playwright, Browser, BrowserContext, Page


class WebHarness:
    def __init__(self, context: dict, seed: str = "core"):
        self.context = context
        self.case_dir = Path(context["case_dir"])
        self.case_dir.mkdir(parents=True, exist_ok=True)
        self.data_dir = self.case_dir / "data"
        self.data_dir.mkdir(parents=True, exist_ok=True)
        self.log_dir = self.case_dir / "server"
        self.log_dir.mkdir(parents=True, exist_ok=True)
        self.bin_path = Path(context["bin_path"])
        self.policy_path = Path(context["policy_path"])
        self.seed = seed
        self.server_proc: Optional[subprocess.Popen] = None
        self.server_log_path = self.log_dir / "server.log"
        self.base_url: Optional[str] = None
        self.server_port: Optional[int] = None
        self.server_pid: Optional[int] = None

        # Playwright
        self._playwright = None
        self.browser: Optional[Browser] = None
        self.browser_context: Optional[BrowserContext] = None
        self.page: Optional[Page] = None
        self.console_messages: List[str] = []
        self.page_errors: List[str] = []
        self.network_events: List[Dict[str, Any]] = []

        # Artifacts
        self.trace_path = self.case_dir / "trace.zip"
        self.screenshot_path = self.case_dir / "screenshot.png"
        self.processes_path = self.case_dir / "processes.json"
        self.state_summary_path = self.case_dir / "state-summary.json"
        self.artifacts: List[str] = []

    def start_server(self, startup_timeout: float = 10.0):
        cmd = [
            str(self.bin_path),
            "serve",
            "--data-dir", str(self.data_dir),
            "--listen", "127.0.0.1:0",
            "--policy", str(self.policy_path),
        ]
        if self.seed:
            cmd.extend(["--seed", self.seed])

        with open(self.server_log_path, "w", encoding="utf-8") as stream:
            self.server_proc = subprocess.Popen(
                cmd,
                stdout=stream,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        self.server_pid = self.server_proc.pid

        # Wait for ready signal
        deadline = time.monotonic() + startup_timeout
        ready_data = None
        pending = ""
        with open(self.server_log_path, "r", encoding="utf-8", errors="replace") as reader:
            while time.monotonic() < deadline:
                pending += reader.read()
                while "\n" in pending:
                    line, pending = pending.split("\n", 1)
                    line = line.strip()
                    if line.startswith("{") and line.endswith("}"):
                        try:
                            data = json.loads(line)
                            if data.get("ready") is True:
                                ready_data = data
                                break
                        except Exception:
                            pass
                if ready_data:
                    break
                if self.server_proc.poll() is not None:
                    break
                time.sleep(0.05)

        if not ready_data:
            self.stop_server()
            raise RuntimeError(f"Server failed to start within {startup_timeout}s; check {self.server_log_path}")

        self.base_url = ready_data["base_url"]
        self.server_port = int(self.base_url.split(":")[-1].split("/")[0])
        return self.base_url

    def start_browser(self):
        self._playwright = sync_playwright().start()
        self.browser = self._playwright.chromium.launch()
        self.browser_context = self.browser.new_context(
            viewport={"width": 1280, "height": 800},
            locale="zh-CN",
        )
        self.browser_context.tracing.start(screenshots=True, snapshots=True, sources=True)
        self.page = self.browser_context.new_page()

        self.page.on("console", lambda msg: self.console_messages.append(f"[{msg.type}] {msg.text}"))
        self.page.on("pageerror", lambda err: self.page_errors.append(str(err)))
        self.page.on("response", lambda resp: self.network_events.append({
            "url": resp.url,
            "status": resp.status,
            "content_type": resp.headers.get("content-type", "")
        }))

    def get_db_summary(self) -> Dict[str, Any]:
        db_path = self.data_dir / "app.db"
        if not db_path.is_file():
            return {"db_exists": False}

        summary = {"db_exists": True, "tables": {}}
        try:
            with closing(sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)) as conn:
                cursor = conn.cursor()
                cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
                tables = [r[0] for r in cursor.fetchall() if not r[0].startswith("sqlite_")]
                for table in tables:
                    try:
                        cursor.execute(f"SELECT COUNT(*) FROM {table}")
                        summary["tables"][table] = cursor.fetchone()[0]
                    except Exception:
                        pass
        except Exception as e:
            summary["error"] = str(e)
        return summary

    def stop_browser(self):
        if self.page and not self.page.is_closed():
            try:
                self.page.screenshot(path=str(self.screenshot_path))
                self.artifacts.append(str(self.screenshot_path))
            except Exception:
                pass
        if self.browser_context:
            try:
                self.browser_context.tracing.stop(path=str(self.trace_path))
                self.artifacts.append(str(self.trace_path))
            except Exception:
                pass
            try:
                self.browser_context.close()
            except Exception:
                pass
        if self.browser:
            try:
                self.browser.close()
            except Exception:
                pass
        if self._playwright:
            try:
                self._playwright.stop()
            except Exception:
                pass

    def stop_server(self):
        if self.server_proc and self.server_proc.poll() is None:
            try:
                os.killpg(self.server_proc.pid, signal.SIGTERM)
                self.server_proc.wait(timeout=3.0)
            except Exception:
                try:
                    os.killpg(self.server_proc.pid, signal.SIGKILL)
                    self.server_proc.wait(timeout=2.0)
                except Exception:
                    pass

    def close(self):
        self.stop_browser()
        self.stop_server()

        # Record processes.json
        exit_code = self.server_proc.returncode if self.server_proc else 0
        proc_info = [{
            "name": "avocab",
            "pid": self.server_pid,
            "port": self.server_port,
            "data_dir": str(self.data_dir),
            "exit_code": exit_code,
        }]
        self.processes_path.write_text(json.dumps(proc_info, indent=2) + "\n", encoding="utf-8")
        self.artifacts.append(str(self.processes_path))

        # Record state-summary.json
        state_summary = self.get_db_summary()
        state_summary["console_messages_count"] = len(self.console_messages)
        state_summary["page_errors_count"] = len(self.page_errors)
        self.state_summary_path.write_text(json.dumps(state_summary, indent=2) + "\n", encoding="utf-8")
        self.artifacts.append(str(self.state_summary_path))

        if self.server_log_path.is_file():
            self.artifacts.append(str(self.server_log_path))

    def __enter__(self):
        self.start_server()
        self.start_browser()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()
